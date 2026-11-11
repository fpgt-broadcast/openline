// Audio engine: I2S <-> WM8960 at 16 kHz, Opus 20 ms frames.
// One task does capture -> encode -> send and decode -> mix -> playback in
// lock-step with the codec clock, so there is no drift between the two.
#include "audio.h"

#include <math.h>
#include <string.h>

#include "driver/i2s_std.h"
#include "esp_heap_caps.h"
#include "esp_log.h"
#include "esp_timer.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "opus.h"

#include "board.h"

static const char *TAG = "audio";

#define MAX_SRC 4
#define PKT_SLOTS 10
#define PKT_MAX 400
#define START_DEPTH 3 // 60 ms jitter buffer

typedef struct {
    uint8_t data[PKT_MAX];
    uint16_t len;
    uint32_t mask;
} pkt_t;

typedef struct {
    bool used;
    uint16_t sid;
    OpusDecoder *dec;
    pkt_t q[PKT_SLOTS];
    int head, count;
    bool primed;
    int misses;
    int64_t last_rx_us;
    int64_t last_play_us;
    uint32_t mask;
    float peak;
} source_t;

static i2s_chan_handle_t s_tx, s_rx;
static OpusEncoder *s_enc;
static source_t *s_src; // MAX_SRC, in PSRAM
static portMUX_TYPE s_lock = portMUX_INITIALIZER_UNLOCKED;
static audio_tx_cb_t s_tx_cb;
static audio_gain_cb_t s_gain_cb;
static volatile bool s_transmit;
static volatile float s_mic_peak;
static uint16_t s_seq;

void audio_set_transmit(bool on) { s_transmit = on; }
float audio_mic_level(void) { float p = s_mic_peak; s_mic_peak = 0; return p; }

uint32_t audio_active_mask(void)
{
    uint32_t m = 0;
    int64_t now = esp_timer_get_time();
    portENTER_CRITICAL(&s_lock);
    for (int i = 0; i < MAX_SRC; i++) {
        if (s_src[i].used && now - s_src[i].last_play_us < 300000) m |= s_src[i].mask;
    }
    portEXIT_CRITICAL(&s_lock);
    return m;
}

void audio_rx(uint16_t sid, uint32_t mask, const uint8_t *opus, int len)
{
    if (len <= 0 || len > PKT_MAX) return;
    int64_t now = esp_timer_get_time();
    portENTER_CRITICAL(&s_lock);
    source_t *s = NULL, *victim = NULL;
    for (int i = 0; i < MAX_SRC; i++) {
        if (s_src[i].used && s_src[i].sid == sid) { s = &s_src[i]; break; }
        if (!s_src[i].used && !victim) victim = &s_src[i];
    }
    if (!s) {
        if (!victim) { // reuse the stalest source idle for > 1 s
            for (int i = 0; i < MAX_SRC; i++) {
                if (now - s_src[i].last_rx_us > 1000000 && (!victim || s_src[i].last_rx_us < victim->last_rx_us)) victim = &s_src[i];
            }
        }
        if (!victim) { portEXIT_CRITICAL(&s_lock); return; } // > MAX_SRC simultaneous talkers: drop
        s = victim;
        s->used = true;
        s->sid = sid;
        s->head = s->count = 0;
        s->primed = false;
        s->misses = 0;
        opus_decoder_ctl(s->dec, OPUS_RESET_STATE);
    }
    if (s->count == PKT_SLOTS) { // overflow: drop oldest to cap latency
        s->head = (s->head + 1) % PKT_SLOTS;
        s->count--;
    }
    pkt_t *p = &s->q[(s->head + s->count) % PKT_SLOTS];
    memcpy(p->data, opus, len);
    p->len = len;
    p->mask = mask;
    s->count++;
    s->last_rx_us = now;
    portEXIT_CRITICAL(&s_lock);
}

static void mix_sources(int32_t *acc)
{
    static pkt_t pkt;
    static int16_t pcm[AUDIO_FRAME];
    int64_t now = esp_timer_get_time();
    for (int i = 0; i < MAX_SRC; i++) {
        source_t *s = &s_src[i];
        bool have = false, plc = false;
        portENTER_CRITICAL(&s_lock);
        if (s->used) {
            if (!s->primed && s->count >= START_DEPTH) s->primed = true;
            if (s->primed) {
                if (s->count > 0) {
                    pkt = s->q[s->head];
                    s->head = (s->head + 1) % PKT_SLOTS;
                    s->count--;
                    s->misses = 0;
                    have = true;
                } else if (++s->misses <= 2) {
                    plc = true; // conceal short gaps
                } else {
                    s->primed = false;
                }
            }
            if (!s->primed && now - s->last_rx_us > 5000000) s->used = false;
        }
        portEXIT_CRITICAL(&s_lock);
        if (!have && !plc) continue;
        int n = opus_decode(s->dec, have ? pkt.data : NULL, have ? pkt.len : 0, pcm, AUDIO_FRAME, 0);
        if (n <= 0) continue;
        uint32_t mask = have ? pkt.mask : s->mask;
        float g = s_gain_cb ? s_gain_cb(mask) : 1.0f;
        int gi = (int)(g * 256);
        float peak = 0;
        for (int k = 0; k < n; k++) {
            acc[k] += (pcm[k] * gi) >> 8;
            float a = fabsf(pcm[k] / 32768.0f);
            if (a > peak) peak = a;
        }
        portENTER_CRITICAL(&s_lock);
        s->mask = mask;
        s->peak = peak;
        s->last_play_us = now;
        portEXIT_CRITICAL(&s_lock);
    }
}

static void audio_task(void *arg)
{
    int16_t *io = heap_caps_malloc(AUDIO_FRAME * 2 * sizeof(int16_t), MALLOC_CAP_INTERNAL | MALLOC_CAP_DMA);
    int16_t mono[AUDIO_FRAME];
    int32_t acc[AUDIO_FRAME];
    uint8_t pkt[PKT_MAX];
    size_t got;
    for (;;) {
        // capture 20 ms (stereo slots, mic is on the left)
        if (i2s_channel_read(s_rx, io, AUDIO_FRAME * 4, &got, portMAX_DELAY) != ESP_OK) continue;
        float peak = 0;
        for (int i = 0; i < AUDIO_FRAME; i++) {
            mono[i] = io[i * 2];
            float a = fabsf(mono[i] / 32768.0f);
            if (a > peak) peak = a;
        }
        if (peak > s_mic_peak) s_mic_peak = peak;
        if (s_transmit && s_tx_cb) {
            int n = opus_encode(s_enc, mono, AUDIO_FRAME, pkt, sizeof(pkt));
            if (n > 0) s_tx_cb(pkt, n, s_seq++);
        }
        // playback
        memset(acc, 0, sizeof(acc));
        mix_sources(acc);
        for (int i = 0; i < AUDIO_FRAME; i++) {
            int32_t v = acc[i];
            if (v > 32767) v = 32767;
            if (v < -32768) v = -32768;
            io[i * 2] = io[i * 2 + 1] = (int16_t)v;
        }
        size_t w;
        i2s_channel_write(s_tx, io, AUDIO_FRAME * 4, &w, portMAX_DELAY);
    }
}

void audio_init(audio_tx_cb_t tx, audio_gain_cb_t gain)
{
    s_tx_cb = tx;
    s_gain_cb = gain;

    i2s_chan_config_t chan = I2S_CHANNEL_DEFAULT_CONFIG(I2S_NUM_0, I2S_ROLE_MASTER);
    chan.dma_desc_num = 4;
    chan.dma_frame_num = AUDIO_FRAME / 2; // 10 ms per DMA buffer
    chan.auto_clear = true;
    ESP_ERROR_CHECK(i2s_new_channel(&chan, &s_tx, &s_rx));
    i2s_std_config_t std = {
        .clk_cfg = I2S_STD_CLK_DEFAULT_CONFIG(AUDIO_RATE), // MCLK = 256 fs = 4.096 MHz
        .slot_cfg = I2S_STD_PHILIPS_SLOT_DEFAULT_CONFIG(I2S_DATA_BIT_WIDTH_16BIT, I2S_SLOT_MODE_STEREO),
        .gpio_cfg = {
            .mclk = PIN_I2S_MCLK, .bclk = PIN_I2S_BCLK, .ws = PIN_I2S_WS,
            .dout = PIN_I2S_DOUT, .din = PIN_I2S_DIN,
        },
    };
    ESP_ERROR_CHECK(i2s_channel_init_std_mode(s_tx, &std));
    ESP_ERROR_CHECK(i2s_channel_init_std_mode(s_rx, &std));
    ESP_ERROR_CHECK(i2s_channel_enable(s_tx));
    ESP_ERROR_CHECK(i2s_channel_enable(s_rx));

    int err;
    s_enc = opus_encoder_create(AUDIO_RATE, 1, OPUS_APPLICATION_VOIP, &err);
    if (err != OPUS_OK) ESP_LOGE(TAG, "opus encoder: %d", err);
    opus_encoder_ctl(s_enc, OPUS_SET_BITRATE(24000));
    opus_encoder_ctl(s_enc, OPUS_SET_COMPLEXITY(3));
    opus_encoder_ctl(s_enc, OPUS_SET_SIGNAL(OPUS_SIGNAL_VOICE));

    s_src = heap_caps_calloc(MAX_SRC, sizeof(source_t), MALLOC_CAP_SPIRAM);
    for (int i = 0; i < MAX_SRC; i++) {
        s_src[i].dec = opus_decoder_create(AUDIO_RATE, 1, &err);
        if (err != OPUS_OK) ESP_LOGE(TAG, "opus decoder: %d", err);
    }
    // libopus needs a large stack (pseudo-stack / VLAs).
    xTaskCreatePinnedToCore(audio_task, "audio", 40 * 1024, NULL, 20, NULL, 1);
    ESP_LOGI(TAG, "running at %d Hz", AUDIO_RATE);
}
