// Talk keys, rotary encoder and the WS2812 key LEDs.
#include "keys.h"

#include "driver/gpio.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "led_strip.h"

#include "board.h"
#include "ui.h"

static const int KEY_PINS[NUM_KEYS] = {PIN_KEY1, PIN_KEY2, PIN_KEY3, PIN_KEY4};
static led_strip_handle_t s_strip;
static uint32_t s_led[NUM_KEYS];
static int s_bright = 80;

void keys_set_led(int i, uint32_t rgb)
{
    if (i >= 0 && i < NUM_KEYS) s_led[i] = rgb;
}

void keys_led_brightness(int pct) { s_bright = pct < 5 ? 5 : pct; }

void keys_led_refresh(void)
{
    if (!s_strip) return;
    for (int i = 0; i < NUM_KEYS; i++) {
        uint32_t c = s_led[i];
        // WS2812s are very bright; cap at ~40 % and apply user brightness
        int k = s_bright * 40 / 100;
        led_strip_set_pixel(s_strip, i, ((c >> 16) & 0xFF) * k / 100, ((c >> 8) & 0xFF) * k / 100, (c & 0xFF) * k / 100);
    }
    led_strip_refresh(s_strip);
}

static void keys_task(void *arg)
{
    bool stable[NUM_KEYS] = {0}, enc_sw = false;
    int cnt[NUM_KEYS] = {0}, enc_cnt = 0;
    // quadrature decoder table: index = (prev << 2) | cur
    static const int8_t QDEC[16] = {0, -1, 1, 0, 1, 0, 0, -1, -1, 0, 0, 1, 0, 1, -1, 0};
    uint8_t prev = (gpio_get_level(PIN_ENC_A) << 1) | gpio_get_level(PIN_ENC_B);
    int acc = 0;
    for (;;) {
        for (int i = 0; i < NUM_KEYS; i++) { // 5 ms debounce
            bool down = gpio_get_level(KEY_PINS[i]) == 0;
            if (down != stable[i]) {
                if (++cnt[i] >= 5) { stable[i] = down; cnt[i] = 0; ui_key_event(i, down); }
            } else cnt[i] = 0;
        }
        bool sw = gpio_get_level(PIN_ENC_SW) == 0;
        if (sw != enc_sw) {
            if (++enc_cnt >= 5) { enc_sw = sw; enc_cnt = 0; ui_enc_button(sw); }
        } else enc_cnt = 0;
        uint8_t cur = (gpio_get_level(PIN_ENC_A) << 1) | gpio_get_level(PIN_ENC_B);
        if (cur != prev) {
            acc += QDEC[(prev << 2) | cur];
            prev = cur;
            if (acc >= 4) { acc = 0; ui_enc_turn(1); }      // one detent = 4 transitions
            else if (acc <= -4) { acc = 0; ui_enc_turn(-1); }
        }
        vTaskDelay(1);
    }
}

void keys_init(void)
{
    uint64_t mask = (1ULL << PIN_ENC_A) | (1ULL << PIN_ENC_B) | (1ULL << PIN_ENC_SW);
    for (int i = 0; i < NUM_KEYS; i++) mask |= 1ULL << KEY_PINS[i];
    gpio_config_t io = {.pin_bit_mask = mask, .mode = GPIO_MODE_INPUT, .pull_up_en = GPIO_PULLUP_ENABLE};
    gpio_config(&io);

    led_strip_config_t sc = {
        .strip_gpio_num = PIN_LEDS,
        .max_leds = NUM_KEYS,
        .led_model = LED_MODEL_WS2812,
        .color_component_format = LED_STRIP_COLOR_COMPONENT_FMT_GRB,
    };
    led_strip_rmt_config_t rc = {.clk_src = RMT_CLK_SRC_DEFAULT, .resolution_hz = 10 * 1000 * 1000};
    if (led_strip_new_rmt_device(&sc, &rc, &s_strip) == ESP_OK) led_strip_clear(s_strip);
    xTaskCreatePinnedToCore(keys_task, "keys", 3072, NULL, 10, NULL, 0);
}
