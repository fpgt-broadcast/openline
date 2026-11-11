#pragma once
#include <stdbool.h>

#include "driver/spi_master.h"

typedef void (*net_ready_cb_t)(void);

// Starts Wi-Fi or Ethernet per g_cfg.mode; `ready` is called each time an IP is obtained.
void net_start(spi_host_device_t spi, net_ready_cb_t ready);
bool net_is_up(void);
int net_rssi(void);
