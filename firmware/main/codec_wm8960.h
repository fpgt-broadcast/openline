#pragma once
#include <stdbool.h>
#include <stdint.h>

#include "driver/i2c_master.h"
#include "esp_err.h"

esp_err_t codec_init(i2c_master_bus_handle_t bus);
void codec_set_mic(bool electret, uint8_t gain_pct);
void codec_set_hp(uint8_t level_pct);
void codec_set_sidetone(uint8_t pct);
void codec_mute_output(bool mute);
