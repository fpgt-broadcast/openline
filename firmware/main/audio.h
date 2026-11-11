#pragma once
#include <stdbool.h>
#include <stdint.h>

// Called from the audio task with each encoded 20 ms mic frame while transmitting.
typedef void (*audio_tx_cb_t)(const uint8_t *opus, int len, uint16_t seq);
// Returns the linear playback gain for audio heard on the channels in `mask`.
typedef float (*audio_gain_cb_t)(uint32_t mask);

void audio_init(audio_tx_cb_t tx, audio_gain_cb_t gain);
void audio_set_transmit(bool on);
// Queue one received Opus packet from talker `sid` (thread safe).
void audio_rx(uint16_t sid, uint32_t mask, const uint8_t *opus, int len);
float audio_mic_level(void);      // peak since last call, 0..1
uint32_t audio_active_mask(void); // channel bits currently audible
