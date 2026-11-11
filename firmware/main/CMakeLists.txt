idf_component_register(
    SRCS "main.c" "settings.c" "codec_wm8960.c" "audio.c" "ui.c" "display.c"
         "keys.c" "power.c" "net.c" "ws_link.c" "ble_link.c"
    INCLUDE_DIRS "."
    REQUIRES nvs_flash esp_wifi esp_eth esp_netif esp_event driver esp_driver_i2s
             esp_driver_i2c esp_driver_gpio esp_driver_spi esp_adc bt json
             mbedtls esp_timer esp_system
)
