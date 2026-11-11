#pragma once

// BLE bridge mode: the beltpack pairs with the OpenLine app on a phone/computer.
void ble_link_start_bridge(void);
// Network modes: advertise for `seconds` so the admin page can (re)provision.
void ble_link_start_provisioning(int seconds);
