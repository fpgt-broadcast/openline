// 1-bit framebuffer + drivers: SH1106 / SSD1306 128x64 OLED (I2C) and
// SSD1680 2.13" 250x122 e-ink (SPI). "on" = lit pixel on OLED, black on e-ink.
#include "display.h"

#include <string.h>

#include "driver/gpio.h"
#include "esp_log.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "sdkconfig.h"

#include "board.h"
#include "fonts.h"

static const char *TAG = "disp";

#if CONFIG_OPENLINE_DISPLAY_SSD1680
#define W 250
#define H 122
#else
#define W 128
#define H 64
#endif
#define STRIDE ((W + 7) / 8)

static uint8_t s_fb[STRIDE * H];

int disp_w(void) { return W; }
int disp_h(void) { return H; }
#if CONFIG_OPENLINE_DISPLAY_SSD1680
bool disp_is_eink(void) { return true; }
#else
bool disp_is_eink(void) { return false; }
#endif

void disp_clear(void) { memset(s_fb, 0, sizeof(s_fb)); }

void disp_pixel(int x, int y, bool on)
{
    if (x < 0 || y < 0 || x >= W || y >= H) return;
    uint8_t *b = &s_fb[y * STRIDE + x / 8];
    uint8_t m = 0x80 >> (x & 7);
    if (on) *b |= m;
    else *b &= ~m;
}

static bool get_px(int x, int y) { return s_fb[y * STRIDE + x / 8] & (0x80 >> (x & 7)); }

void disp_fill(int x, int y, int w, int h, bool on)
{
    for (int j = y; j < y + h; j++)
        for (int i = x; i < x + w; i++) disp_pixel(i, j, on);
}

void disp_rect(int x, int y, int w, int h, bool on)
{
    for (int i = x; i < x + w; i++) { disp_pixel(i, y, on); disp_pixel(i, y + h - 1, on); }
    for (int j = y; j < y + h; j++) { disp_pixel(x, j, on); disp_pixel(x + w - 1, j, on); }
}

void disp_invert_all(void)
{
    for (size_t i = 0; i < sizeof(s_fb); i++) s_fb[i] = ~s_fb[i];
}

void disp_text(int x, int y, const char *s, bool big, bool inverted)
{
    int cw = big ? 8 : 6, ch = big ? 16 : 8, pages = big ? 2 : 1;
    for (; *s; s++, x += cw) {
        unsigned c = (unsigned char)*s;
        if (c < 32 || c > 126) c = '?';
        const uint8_t *g = big ? font_big[c - 32] : font_small[c - 32];
        for (int gx = 0; gx < cw; gx++)
            for (int gy = 0; gy < ch; gy++)
                if (g[gx * pages + gy / 8] & (1 << (gy & 7))) disp_pixel(x + gx, y + gy, !inverted);
    }
}

#if !CONFIG_OPENLINE_DISPLAY_SSD1680
// ---------------- OLED (I2C) ----------------
static i2c_master_dev_handle_t s_oled;
#if CONFIG_OPENLINE_DISPLAY_SH1106
#define COL_OFFSET 2
#else
#define COL_OFFSET 0
#endif

static void oled_cmds(const uint8_t *c, size_t n)
{
    uint8_t buf[32];
    buf[0] = 0x00;
    memcpy(buf + 1, c, n);
    i2c_master_transmit(s_oled, buf, n + 1, 50);
}

void disp_init(i2c_master_bus_handle_t bus, spi_host_device_t spi)
{
    (void)spi;
    i2c_device_config_t cfg = {.dev_addr_length = I2C_ADDR_BIT_LEN_7, .device_address = I2C_ADDR_OLED, .scl_speed_hz = 400000};
    ESP_ERROR_CHECK(i2c_master_bus_add_device(bus, &cfg, &s_oled));
#if CONFIG_OPENLINE_DISPLAY_SH1106
    static const uint8_t init[] = {0xAE, 0xD5, 0x80, 0xA8, 0x3F, 0xD3, 0x00, 0x40, 0xAD, 0x8B, 0xA1, 0xC8,
                                   0xDA, 0x12, 0x81, 0xC0, 0xD9, 0x22, 0xDB, 0x35, 0xA4, 0xA6, 0xAF};
#else
    static const uint8_t init[] = {0xAE, 0xD5, 0x80, 0xA8, 0x3F, 0xD3, 0x00, 0x40, 0x8D, 0x14, 0x20, 0x02, 0xA1,
                                   0xC8, 0xDA, 0x12, 0x81, 0xC0, 0xD9, 0xF1, 0xDB, 0x40, 0xA4, 0xA6, 0xAF};
#endif
    oled_cmds(init, sizeof(init));
    disp_clear();
    disp_flush();
    ESP_LOGI(TAG, "OLED ready");
}

void disp_contrast(int pct)
{
    uint8_t c[2] = {0x81, (uint8_t)(pct * 255 / 100)};
    oled_cmds(c, 2);
}

void disp_flush(void)
{
    uint8_t buf[1 + W];
    for (int p = 0; p < H / 8; p++) {
        uint8_t cmd[3] = {(uint8_t)(0xB0 | p), (uint8_t)(COL_OFFSET & 0x0F), (uint8_t)(0x10 | (COL_OFFSET >> 4))};
        oled_cmds(cmd, 3);
        buf[0] = 0x40;
        for (int x = 0; x < W; x++) {
            uint8_t b = 0;
            for (int bit = 0; bit < 8; bit++)
                if (get_px(x, p * 8 + bit)) b |= 1 << bit;
            buf[1 + x] = b;
        }
        i2c_master_transmit(s_oled, buf, sizeof(buf), 50);
    }
}

#else
// ---------------- e-ink SSD1680 (SPI) — experimental, verify on hardware ----------------
static spi_device_handle_t s_epd;
static int s_partials = -1; // -1 = next refresh is full
#define EPD_ROWS 250
#define EPD_BYTES 16 // 128 bits per row, 122 visible
static uint8_t s_native[EPD_ROWS * EPD_BYTES];

static void epd_wait(void)
{
    for (int i = 0; i < 500 && gpio_get_level(PIN_EPD_BUSY); i++) vTaskDelay(pdMS_TO_TICKS(10));
}

static void epd_send(bool data, const uint8_t *b, size_t n)
{
    gpio_set_level(PIN_EPD_DC, data);
    while (n) {
        size_t chunk = n > 4000 ? 4000 : n;
        spi_transaction_t t = {.length = chunk * 8, .tx_buffer = b};
        spi_device_polling_transmit(s_epd, &t);
        b += chunk;
        n -= chunk;
    }
}
static void epd_cmd(uint8_t c) { epd_send(false, &c, 1); }
static void epd_data(const uint8_t *b, size_t n) { epd_send(true, b, n); }
static void epd_cmd1(uint8_t c, uint8_t d) { epd_cmd(c); epd_data(&d, 1); }

void disp_init(i2c_master_bus_handle_t bus, spi_host_device_t spi)
{
    (void)bus;
    gpio_config_t o = {.pin_bit_mask = (1ULL << PIN_EPD_DC) | (1ULL << PIN_EPD_RST), .mode = GPIO_MODE_OUTPUT};
    gpio_config(&o);
    gpio_config_t in = {.pin_bit_mask = 1ULL << PIN_EPD_BUSY, .mode = GPIO_MODE_INPUT};
    gpio_config(&in);
    spi_device_interface_config_t dev = {.clock_speed_hz = 4 * 1000 * 1000, .mode = 0, .spics_io_num = PIN_EPD_CS, .queue_size = 2};
    ESP_ERROR_CHECK(spi_bus_add_device(spi, &dev, &s_epd));
    gpio_set_level(PIN_EPD_RST, 0);
    vTaskDelay(pdMS_TO_TICKS(10));
    gpio_set_level(PIN_EPD_RST, 1);
    vTaskDelay(pdMS_TO_TICKS(10));
    epd_wait();
    epd_cmd(0x12); // SW reset
    epd_wait();
    epd_cmd(0x01); epd_data((const uint8_t[]){0xF9, 0x00, 0x00}, 3); // 250 gate lines
    epd_cmd1(0x11, 0x03);                                         // X+, Y+
    epd_cmd(0x44); epd_data((const uint8_t[]){0x00, 0x0F}, 2);
    epd_cmd(0x45); epd_data((const uint8_t[]){0x00, 0x00, 0xF9, 0x00}, 4);
    epd_cmd1(0x3C, 0x05); // border
    epd_cmd(0x21); epd_data((const uint8_t[]){0x00, 0x80}, 2);
    epd_cmd1(0x18, 0x80); // internal temperature sensor
    disp_clear();
    disp_flush();
    ESP_LOGI(TAG, "e-ink ready");
}

void disp_contrast(int pct) { (void)pct; }

void disp_flush(void)
{
    // landscape (lx, ly) -> native row ny = lx, column nx = 121 - ly; bit 1 = white
    memset(s_native, 0xFF, sizeof(s_native));
    for (int ly = 0; ly < H; ly++)
        for (int lx = 0; lx < W; lx++) {
            if (!get_px(lx, ly)) continue;
#if CONFIG_OPENLINE_EPD_FLIP
            int ny = EPD_ROWS - 1 - lx, nx = ly;
#else
            int ny = lx, nx = 121 - ly;
#endif
            s_native[ny * EPD_BYTES + nx / 8] &= ~(0x80 >> (nx & 7));
        }
    epd_cmd1(0x4E, 0x00);
    epd_cmd(0x4F); epd_data((const uint8_t[]){0x00, 0x00}, 2);
    epd_cmd(0x24);
    epd_data(s_native, sizeof(s_native));
    bool full = s_partials < 0 || s_partials >= 30;
    if (full) { // also load "previous" RAM so the next partial diff is correct
        epd_cmd1(0x4E, 0x00);
        epd_cmd(0x4F); epd_data((const uint8_t[]){0x00, 0x00}, 2);
        epd_cmd(0x26);
        epd_data(s_native, sizeof(s_native));
    }
    epd_cmd1(0x22, full ? 0xF7 : 0xFC);
    epd_cmd(0x20);
    epd_wait();
    if (!full) { // previous := current
        epd_cmd1(0x4E, 0x00);
        epd_cmd(0x4F); epd_data((const uint8_t[]){0x00, 0x00}, 2);
        epd_cmd(0x26);
        epd_data(s_native, sizeof(s_native));
    }
    s_partials = full ? 0 : s_partials + 1;
}
#endif
