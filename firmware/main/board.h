// OpenLine beltpack pin map — ESP32-S3-WROOM-1-N16R8.
// Keep in sync with hardware/README.md. GPIO 26-37 are used by flash/PSRAM,
// 19/20 by USB (console + flashing), 45/46 are strapping pins and unused.
#pragma once

// Audio codec (WM8960) — I2S
#define PIN_I2S_MCLK 38
#define PIN_I2S_BCLK 39
#define PIN_I2S_WS 40
#define PIN_I2S_DOUT 41 // ESP -> codec DACDAT
#define PIN_I2S_DIN 42  // codec ADCDAT -> ESP

// I2C bus: codec (0x1A) + OLED (0x3C)
#define PIN_I2C_SDA 8
#define PIN_I2C_SCL 9
#define I2C_ADDR_WM8960 0x1A
#define I2C_ADDR_OLED 0x3C

// SPI bus: W5500 Ethernet and/or e-ink
#define PIN_SPI_MOSI 11
#define PIN_SPI_SCK 12
#define PIN_SPI_MISO 13
#define PIN_ETH_CS 14
#define PIN_ETH_INT 15
#define PIN_ETH_RST 16
#define PIN_EPD_CS 10
#define PIN_EPD_DC 43
#define PIN_EPD_RST 48
#define PIN_EPD_BUSY 3

// User input
#define PIN_KEY1 4 // talk keys, active low, internal pull-ups
#define PIN_KEY2 5
#define PIN_KEY3 6
#define PIN_KEY4 7
#define PIN_ENC_A 17
#define PIN_ENC_B 18
#define PIN_ENC_SW 21

// WS2812 RGB LEDs under the 4 talk keys (chain of 4)
#define PIN_LEDS 47
#define NUM_KEYS 4

// Power / sensing
#define PIN_VBAT_ADC 1  // ADC1_CH0, battery via 100k/100k divider
#define PIN_HS_SENSE 2  // ADC1_CH1, headset mic DC sense (see hardware doc)
#define PIN_CHG_STAT 44 // charger PG/STAT, open drain, low = external power present

#define AUDIO_RATE 16000
#define AUDIO_FRAME 320 // 20 ms @ 16 kHz
