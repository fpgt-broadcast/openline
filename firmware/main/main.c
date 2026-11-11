// OpenLine beltpack firmware entry point.
//
// Modes (set from the admin page, the BT setup flow or the on-device menu):
//   BLE      — pairs with the OpenLine app on a phone/computer (keys + screen,
//              and either beltpack headset audio or phone audio/AirPods)
//   Wi-Fi    — standalone, connects straight to the server
//   Ethernet — standalone over W5500, powered by PoE on the PoE variant
// Hold the encoder button while powering on to force Bluetooth setup.
#include <stdio.h>
#include <string.h>

#include "driver/gpio.h"
#include "driver/i2c_master.h"
#include "driver/spi_master.h"
#include "esp_event.h"
#include "esp_log.h"
#include "esp_netif.h"
#include "sdkconfig.h"

#include "ble_link.h"
#include "board.h"
#include "codec_wm8960.h"
#include "display.h"
#include "keys.h"
#include "net.h"
#include "power.h"
#include "settings.h"
#include "ui.h"
#include "ws_link.h"

static const char *TAG = "main";

static void on_net_ready(void)
{
    ui_lock();
    snprintf(g_ui.status, sizeof(g_ui.status), "NO SERVER");
    g_ui.conn = CONN_CONNECTING;
    ui_unlock();
    ui_changed();
    ws_link_start();
}

void app_main(void)
{
    settings_load();
    ESP_ERROR_CHECK(esp_netif_init());
    ESP_ERROR_CHECK(esp_event_loop_create_default());

    i2c_master_bus_config_t i2c = {
        .i2c_port = I2C_NUM_0,
        .sda_io_num = PIN_I2C_SDA,
        .scl_io_num = PIN_I2C_SCL,
        .clk_source = I2C_CLK_SRC_DEFAULT,
        .glitch_ignore_cnt = 7,
        .flags.enable_internal_pullup = true,
    };
    i2c_master_bus_handle_t bus;
    ESP_ERROR_CHECK(i2c_new_master_bus(&i2c, &bus));

#if CONFIG_OPENLINE_ETHERNET || CONFIG_OPENLINE_DISPLAY_SSD1680
    spi_bus_config_t spi = {
        .mosi_io_num = PIN_SPI_MOSI, .miso_io_num = PIN_SPI_MISO, .sclk_io_num = PIN_SPI_SCK,
        .quadwp_io_num = -1, .quadhd_io_num = -1, .max_transfer_sz = 4096,
    };
    ESP_ERROR_CHECK(spi_bus_initialize(SPI2_HOST, &spi, SPI_DMA_CH_AUTO));
#endif

    if (codec_init(bus) != ESP_OK) ESP_LOGE(TAG, "codec not responding — check I2C wiring");
    disp_init(bus, SPI2_HOST);
    ui_init();
    keys_init();
    power_init();

    gpio_config_t sw = {.pin_bit_mask = 1ULL << PIN_ENC_SW, .mode = GPIO_MODE_INPUT, .pull_up_en = GPIO_PULLUP_ENABLE};
    gpio_config(&sw);
    bool force_setup = gpio_get_level(PIN_ENC_SW) == 0;

    if (g_cfg.name[0]) {
        ui_lock(); snprintf(g_ui.user, sizeof(g_ui.user), "%s", g_cfg.name); ui_unlock();
    }

    if (g_cfg.mode == MODE_BLE && !force_setup) {
        ESP_LOGI(TAG, "Bluetooth bridge mode");
        ble_link_start_bridge();
    } else if (force_setup || !settings_is_network_configured()) {
        ESP_LOGI(TAG, "not configured: Bluetooth setup");
        ui_lock(); snprintf(g_ui.status, sizeof(g_ui.status), "BT SETUP"); ui_unlock();
        ble_link_start_provisioning(24 * 3600);
    } else {
        ESP_LOGI(TAG, "%s mode, server %s", g_cfg.mode == MODE_ETH ? "Ethernet" : "Wi-Fi", g_cfg.server);
        net_start(SPI2_HOST, on_net_ready);
        ble_link_start_provisioning(120); // short window after boot to re-provision
    }
    ui_changed();
}
