// Battery voltage, external power (USB-C / PoE) and headset detection.
#include "power.h"

#include "driver/gpio.h"
#include "esp_adc/adc_cali.h"
#include "esp_adc/adc_cali_scheme.h"
#include "esp_adc/adc_oneshot.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"

#include "board.h"
#include "ui.h"

static adc_oneshot_unit_handle_t s_adc;
static adc_cali_handle_t s_cali;

static int read_mv(adc_channel_t ch)
{
    int raw = 0, mv = 0, sum = 0;
    for (int i = 0; i < 8; i++) {
        adc_oneshot_read(s_adc, ch, &raw);
        if (s_cali) adc_cali_raw_to_voltage(s_cali, raw, &mv);
        else mv = raw * 3100 / 4095;
        sum += mv;
    }
    return sum / 8;
}

// Li-ion open-circuit voltage -> state of charge (rough, under ~150 mA load)
static int li_pct(int mv)
{
    static const int tbl[][2] = {{4150, 100}, {4050, 90}, {3950, 78}, {3870, 65}, {3800, 52}, {3750, 40},
                                 {3700, 28}, {3650, 18}, {3580, 10}, {3450, 4}, {3300, 0}};
    if (mv >= tbl[0][0]) return 100;
    for (int i = 1; i < 11; i++)
        if (mv >= tbl[i][0]) return tbl[i][1] + (mv - tbl[i][0]) * (tbl[i - 1][1] - tbl[i][1]) / (tbl[i - 1][0] - tbl[i][0]);
    return 0;
}

static void power_task(void *arg)
{
    int filt = -1;
    for (;;) {
        int vbat = read_mv(ADC_CHANNEL_0) * 2; // 1:2 divider
        filt = filt < 0 ? vbat : (filt * 7 + vbat) / 8;
        bool ext = gpio_get_level(PIN_CHG_STAT) == 0;
        int hs = read_mv(ADC_CHANNEL_1);
        ui_lock();
        // No battery fitted (PoE / desk unit): report external power.
        g_ui.battery = (filt < 2500) ? -1 : li_pct(filt);
        g_ui.charging = ext && g_ui.battery >= 0 && g_ui.battery < 100;
        g_ui.headset = hs < 2500; // dynamic/electret capsule pulls the sense line down
        ui_unlock();
        ui_changed();
        vTaskDelay(pdMS_TO_TICKS(2000));
    }
}

bool power_external(void) { return gpio_get_level(PIN_CHG_STAT) == 0; }

void power_init(void)
{
    gpio_config_t io = {.pin_bit_mask = 1ULL << PIN_CHG_STAT, .mode = GPIO_MODE_INPUT, .pull_up_en = GPIO_PULLUP_ENABLE};
    gpio_config(&io);
    adc_oneshot_unit_init_cfg_t u = {.unit_id = ADC_UNIT_1};
    ESP_ERROR_CHECK(adc_oneshot_new_unit(&u, &s_adc));
    adc_oneshot_chan_cfg_t c = {.atten = ADC_ATTEN_DB_12, .bitwidth = ADC_BITWIDTH_DEFAULT};
    adc_oneshot_config_channel(s_adc, ADC_CHANNEL_0, &c); // GPIO1 VBAT
    adc_oneshot_config_channel(s_adc, ADC_CHANNEL_1, &c); // GPIO2 headset sense
    adc_cali_curve_fitting_config_t cc = {.unit_id = ADC_UNIT_1, .chan = ADC_CHANNEL_0, .atten = ADC_ATTEN_DB_12, .bitwidth = ADC_BITWIDTH_DEFAULT};
    if (adc_cali_create_scheme_curve_fitting(&cc, &s_cali) != ESP_OK) s_cali = NULL;
    xTaskCreate(power_task, "power", 3072, NULL, 3, NULL);
}
