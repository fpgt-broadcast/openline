#pragma once
#include <stdbool.h>

#include "driver/i2c_master.h"
#include "driver/spi_master.h"

void disp_init(i2c_master_bus_handle_t bus, spi_host_device_t spi);
int disp_w(void);
int disp_h(void);
bool disp_is_eink(void);
void disp_clear(void);
void disp_pixel(int x, int y, bool on);
void disp_fill(int x, int y, int w, int h, bool on);
void disp_rect(int x, int y, int w, int h, bool on);
// big = 8x16 font, else 6x8. inverted = draw "off" pixels (text on a filled area).
void disp_text(int x, int y, const char *s, bool big, bool inverted);
void disp_invert_all(void);
void disp_flush(void);
void disp_contrast(int pct);
