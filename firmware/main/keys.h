#pragma once
#include <stdint.h>

void keys_init(void);
void keys_set_led(int i, uint32_t rgb);
void keys_led_refresh(void);
void keys_led_brightness(int pct);
