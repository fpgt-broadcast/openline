// Network bring-up: Wi-Fi station or W5500 Ethernet (PoE variant), with DHCP
// or the static address pushed from the admin page.
#include "net.h"

#include <stdio.h>
#include <string.h>

#include "driver/gpio.h"
#include "driver/spi_master.h"
#include "esp_eth.h"
#include "esp_event.h"
#include "esp_log.h"
#include "esp_mac.h"
#include "esp_netif.h"
#include "esp_wifi.h"
#include "sdkconfig.h"

#include "board.h"
#include "settings.h"
#include "ui.h"

static const char *TAG = "net";
static esp_netif_t *s_netif;
static net_ready_cb_t s_ready;
static bool s_up;

bool net_is_up(void) { return s_up; }

static void apply_static(esp_netif_t *n)
{
    if (g_cfg.dhcp) return;
    esp_netif_dhcpc_stop(n);
    esp_netif_ip_info_t ip = {0};
    ip.ip.addr = esp_ip4addr_aton(g_cfg.ip);
    ip.netmask.addr = esp_ip4addr_aton(g_cfg.mask);
    ip.gw.addr = esp_ip4addr_aton(g_cfg.gw);
    esp_netif_set_ip_info(n, &ip);
    if (g_cfg.dns[0] || g_cfg.gw[0]) {
        esp_netif_dns_info_t dns = {0};
        dns.ip.type = ESP_IPADDR_TYPE_V4;
        dns.ip.u_addr.ip4.addr = esp_ip4addr_aton(g_cfg.dns[0] ? g_cfg.dns : g_cfg.gw);
        esp_netif_set_dns_info(n, ESP_NETIF_DNS_MAIN, &dns);
    }
    ESP_LOGI(TAG, "static IP %s/%s gw %s", g_cfg.ip, g_cfg.mask, g_cfg.gw);
}

static void on_event(void *arg, esp_event_base_t base, int32_t id, void *data)
{
    if (base == WIFI_EVENT && id == WIFI_EVENT_STA_START) {
        esp_wifi_connect();
    } else if (base == WIFI_EVENT && id == WIFI_EVENT_STA_CONNECTED) {
        apply_static(s_netif);
        if (!g_cfg.dhcp) { // no IP_EVENT for static: signal ready here
            ui_lock(); strncpy(g_ui.ip, g_cfg.ip, sizeof(g_ui.ip) - 1); ui_unlock();
            s_up = true;
            if (s_ready) s_ready();
        }
    } else if (base == WIFI_EVENT && id == WIFI_EVENT_STA_DISCONNECTED) {
        s_up = false;
        ui_lock(); strcpy(g_ui.status, "NO WIFI"); g_ui.conn = CONN_OFFLINE; ui_unlock();
        ui_changed();
        esp_wifi_connect();
    } else if (base == ETH_EVENT && id == ETHERNET_EVENT_CONNECTED) {
        apply_static(s_netif);
        if (!g_cfg.dhcp) {
            ui_lock(); strncpy(g_ui.ip, g_cfg.ip, sizeof(g_ui.ip) - 1); ui_unlock();
            s_up = true;
            if (s_ready) s_ready();
        }
    } else if (base == ETH_EVENT && id == ETHERNET_EVENT_DISCONNECTED) {
        s_up = false;
        ui_lock(); strcpy(g_ui.status, "NO LINK"); g_ui.conn = CONN_OFFLINE; ui_unlock();
        ui_changed();
    } else if (base == IP_EVENT && (id == IP_EVENT_STA_GOT_IP || id == IP_EVENT_ETH_GOT_IP)) {
        ip_event_got_ip_t *e = data;
        ui_lock();
        esp_ip4addr_ntoa(&e->ip_info.ip, g_ui.ip, sizeof(g_ui.ip));
        ui_unlock();
        ESP_LOGI(TAG, "got IP %s", g_ui.ip);
        s_up = true;
        if (s_ready) s_ready();
    }
}

static void start_wifi(void)
{
    s_netif = esp_netif_create_default_wifi_sta();
    wifi_init_config_t cfg = WIFI_INIT_CONFIG_DEFAULT();
    ESP_ERROR_CHECK(esp_wifi_init(&cfg));
    esp_event_handler_register(WIFI_EVENT, ESP_EVENT_ANY_ID, on_event, NULL);
    esp_event_handler_register(IP_EVENT, IP_EVENT_STA_GOT_IP, on_event, NULL);
    wifi_config_t wc = {0};
    strncpy((char *)wc.sta.ssid, g_cfg.ssid, sizeof(wc.sta.ssid));
    strncpy((char *)wc.sta.password, g_cfg.pass, sizeof(wc.sta.password));
    wc.sta.threshold.authmode = g_cfg.pass[0] ? WIFI_AUTH_WPA2_PSK : WIFI_AUTH_OPEN;
    wc.sta.pmf_cfg.capable = true;
    esp_wifi_set_mode(WIFI_MODE_STA);
    esp_wifi_set_config(WIFI_IF_STA, &wc);
    esp_wifi_start();
    esp_wifi_set_ps(WIFI_PS_NONE); // lowest latency; costs ~30 mA
    char host[32];
    snprintf(host, sizeof(host), "openline-%s", settings_device_id() + 6);
    esp_netif_set_hostname(s_netif, host);
}

#if CONFIG_OPENLINE_ETHERNET
static void start_eth(spi_host_device_t spi)
{
    esp_netif_config_t ncfg = ESP_NETIF_DEFAULT_ETH();
    s_netif = esp_netif_new(&ncfg);
    gpio_install_isr_service(0);
    spi_device_interface_config_t dev = {.mode = 0, .clock_speed_hz = 20 * 1000 * 1000, .queue_size = 20, .spics_io_num = PIN_ETH_CS};
    eth_w5500_config_t w5500 = ETH_W5500_DEFAULT_CONFIG(spi, &dev);
    w5500.int_gpio_num = PIN_ETH_INT;
    eth_mac_config_t mac_cfg = ETH_MAC_DEFAULT_CONFIG();
    eth_phy_config_t phy_cfg = ETH_PHY_DEFAULT_CONFIG();
    phy_cfg.reset_gpio_num = PIN_ETH_RST;
    esp_eth_mac_t *mac = esp_eth_mac_new_w5500(&w5500, &mac_cfg);
    esp_eth_phy_t *phy = esp_eth_phy_new_w5500(&phy_cfg);
    esp_eth_config_t ecfg = ETH_DEFAULT_CONFIG(mac, phy);
    esp_eth_handle_t eth = NULL;
    if (esp_eth_driver_install(&ecfg, &eth) != ESP_OK) {
        ESP_LOGE(TAG, "W5500 not found");
        ui_lock(); strcpy(g_ui.status, "NO ETH CHIP"); ui_unlock();
        return;
    }
    uint8_t m[6];
    esp_read_mac(m, ESP_MAC_ETH);
    esp_eth_ioctl(eth, ETH_CMD_S_MAC_ADDR, m);
    esp_netif_attach(s_netif, esp_eth_new_netif_glue(eth));
    esp_event_handler_register(ETH_EVENT, ESP_EVENT_ANY_ID, on_event, NULL);
    esp_event_handler_register(IP_EVENT, IP_EVENT_ETH_GOT_IP, on_event, NULL);
    esp_eth_start(eth);
}
#endif

void net_start(spi_host_device_t spi, net_ready_cb_t ready)
{
    s_ready = ready;
    ui_lock(); strcpy(g_ui.status, "CONNECTING"); ui_unlock();
#if CONFIG_OPENLINE_ETHERNET
    if (g_cfg.mode == MODE_ETH) { start_eth(spi); return; }
#else
    (void)spi;
#endif
    start_wifi();
}

int net_rssi(void)
{
    wifi_ap_record_t ap;
    if (g_cfg.mode == MODE_WIFI && esp_wifi_sta_get_ap_info(&ap) == ESP_OK) return ap.rssi;
    return 0;
}
