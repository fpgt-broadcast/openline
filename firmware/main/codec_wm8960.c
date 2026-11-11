// WM8960 setup for an intercom headset: differential mic on LINPUT1(-)/LINPUT2(+)
// -> left PGA -> boost -> ADC; DAC (mono-mixed) -> LOUT1/ROUT1 headphone amp.
// Sidetone uses the analog bypass (boost mixer -> output mixer): zero latency.
// I2S slave, 16-bit, MCLK = 256 * 16 kHz = 4.096 MHz from the ESP32-S3.
#include "codec_wm8960.h"

#include "esp_log.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"

#include "board.h"

static const char *TAG = "wm8960";
static i2c_master_dev_handle_t s_dev;
static uint16_t s_regs[56]; // shadow (WM8960 is write-only over I2C)

enum {
    R_LINVOL = 0x00, R_RINVOL = 0x01, R_LOUT1 = 0x02, R_ROUT1 = 0x03, R_CLOCK1 = 0x04,
    R_DACCTL1 = 0x05, R_IFACE1 = 0x07, R_LDAC = 0x0A, R_RDAC = 0x0B, R_RESET = 0x0F,
    R_LADC = 0x15, R_RADC = 0x16, R_ADDCTL1 = 0x17, R_PWR1 = 0x19, R_PWR2 = 0x1A,
    R_ANTIPOP1 = 0x1C, R_ADCL_PATH = 0x20, R_ADCR_PATH = 0x21, R_LOUTMIX = 0x22,
    R_ROUTMIX = 0x25, R_LINBOOST = 0x2B, R_BYPASS1 = 0x2D, R_BYPASS2 = 0x2E, R_PWR3 = 0x2F,
};

static esp_err_t wr(uint8_t reg, uint16_t val)
{
    uint8_t b[2] = {(uint8_t)((reg << 1) | ((val >> 8) & 1)), (uint8_t)(val & 0xFF)};
    if (reg < 56) s_regs[reg] = val;
    esp_err_t err = i2c_master_transmit(s_dev, b, 2, 50);
    if (err != ESP_OK) ESP_LOGE(TAG, "write R%02x failed: %s", reg, esp_err_to_name(err));
    return err;
}

esp_err_t codec_init(i2c_master_bus_handle_t bus)
{
    i2c_device_config_t cfg = {
        .dev_addr_length = I2C_ADDR_BIT_LEN_7,
        .device_address = I2C_ADDR_WM8960,
        .scl_speed_hz = 400000,
    };
    ESP_ERROR_CHECK(i2c_master_bus_add_device(bus, &cfg, &s_dev));
    if (wr(R_RESET, 0) != ESP_OK) return ESP_FAIL;
    vTaskDelay(pdMS_TO_TICKS(10));

    wr(R_ANTIPOP1, 0x094);                 // POBCTRL | BUFDCOPEN | SOFT_ST during power-up
    wr(R_PWR1, 0x0E8);                     // VMID 50k, VREF, AINL, ADCL (MICB set later)
    vTaskDelay(pdMS_TO_TICKS(300));
    wr(R_PWR2, 0x1E0);                     // DACL, DACR, LOUT1, ROUT1
    wr(R_PWR3, 0x02C);                     // LMIC (left PGA), LOMIX, ROMIX
    wr(R_ANTIPOP1, 0x000);

    wr(R_CLOCK1, 0x000);                   // SYSCLK = MCLK, ADC/DAC fs = SYSCLK/256 = 16 kHz
    wr(R_IFACE1, 0x002);                   // slave, I2S, 16-bit
    wr(R_DACCTL1, 0x000);                  // DAC unmuted, ADC high-pass on
    wr(R_ADDCTL1, 0x1C0 | 0x10 | (1 << 2)); // thermal shutdown, 3.3 V, DAC mono mix, left ADC on both channels
    wr(R_LADC, 0x1C3);                     // ADC 0 dB
    wr(R_RADC, 0x1C3);
    wr(R_LDAC, 0x1FF);                     // DAC 0 dB
    wr(R_RDAC, 0x1FF);
    wr(R_RINVOL, 0x180);                   // right input muted (unused)
    wr(R_LOUTMIX, 0x100);                  // LD2LO
    wr(R_ROUTMIX, 0x100);                  // RD2RO
    wr(R_LINBOOST, 0x000);                 // LINPUT2/3 boost-mixer paths off (mic goes via PGA)
    codec_set_mic(false, 70);
    codec_set_hp(80);
    codec_set_sidetone(20);
    ESP_LOGI(TAG, "initialised");
    return ESP_OK;
}

void codec_set_mic(bool electret, uint8_t gain_pct)
{
    // Dynamic headset mics are ~-55 dBV: use +29 dB boost and up to +30 dB PGA.
    // Electrets are ~20 dB hotter: +13 dB boost and enable mic bias.
    uint16_t boost = electret ? 1 : 3; // 01 = +13 dB, 11 = +29 dB
    wr(R_ADCL_PATH, 0x100 | 0x040 | (boost << 4) | 0x008); // LMN1, LMP2, boost, LMIC2B
    uint16_t vol = (uint16_t)(gain_pct * 0x3F / 100);       // -17.25 .. +30 dB
    wr(R_LINVOL, 0x100 | 0x040 | vol);                     // IPVU | LIZC | vol (unmuted)
    uint16_t pwr1 = s_regs[R_PWR1] & ~0x002;
    wr(R_PWR1, pwr1 | (electret ? 0x002 : 0));
}

void codec_set_hp(uint8_t level_pct)
{
    // 0x30 = mute, 0x79 = 0 dB, 0x7F = +6 dB
    uint16_t v = level_pct == 0 ? 0x30 : (uint16_t)(0x40 + level_pct * 0x3F / 100);
    wr(R_LOUT1, 0x100 | 0x080 | v);
    wr(R_ROUT1, 0x100 | 0x080 | v);
}

void codec_set_sidetone(uint8_t pct)
{
    // Analog bypass LB2LO / (left boost to right mixer via BYPASS2 is not available,
    // so the right ear gets sidetone through the mono DAC only). 0 dB .. -21 dB.
    if (pct == 0) {
        wr(R_BYPASS1, 0x000);
        wr(R_BYPASS2, 0x000);
        return;
    }
    uint16_t att = (uint16_t)(7 - (pct * 7 / 100)); // 0 = 0 dB, 7 = -21 dB
    wr(R_BYPASS1, 0x080 | (att << 4));
}

void codec_mute_output(bool mute)
{
    uint16_t v = s_regs[R_DACCTL1];
    wr(R_DACCTL1, mute ? (v | 0x008) : (v & ~0x008));
}
