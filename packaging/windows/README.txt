OpenLine bridge for Windows
===========================

Connects your venue's NDI audio (program feed, announcer talkback) and vMix
tally to your OpenLine intercom server, and plays intercom channels out at
the venue as NDI sources and/or on sound card outputs. It needs no inbound ports or port
forwarding: the bridge connects out to the server.

What's in this folder
  openline-bridge.exe     the bridge
  opus.dll                audio codec (keep it next to the .exe)
  portaudio.dll           sound card access, for channel outputs (keep it next to the .exe)
  bridge.example.json     example settings
  run-bridge.bat          double-click to start the bridge
  list-ndi-sources.bat    double-click to see NDI sources on the network
  list-sound-cards.bat    double-click to see sound card outputs (for "outputs" > "card")

Setup
  1. Install "NDI Tools" (free) from https://ndi.video/tools/ on this PC.
     It includes the NDI runtime the bridge uses. Skip this if you only
     want vMix tally.
  2. Double-click list-ndi-sources.bat and note the exact source names,
     e.g.  PRODUCTION-PC (Program)  and  BOOTH-PC (Talkback).
  3. Copy bridge.example.json to bridge.json and edit it in Notepad:
       - "server": your intercom address, e.g. https://intercom.yourdomain.com
       - each "token": in OpenLine Admin > Users > Edit, copy the token of
         the "Program Feed" / "Announcer Link" users
       - "source": the NDI source names from step 2
       - "vmix" > "host": the vMix PC's IP address (remove the "vmix" line
         if you don't use vMix)
  4. Double-click run-bridge.bat. Every 10 seconds each link prints a status
     line, e.g.  [Announcers] online · in peak -18 dB ...
  5. In the announcers' NDI mixer / NDI Tools, pick the new source
     "OpenLine IFB Announcers" for their headphones.

Channel outputs (each channel as its own NDI source and/or sound card output)
  1. Admin > Users: add a user and tick "channel outputs". Save, then Edit it
     and copy its token.
  2. Admin > Channels: in the Output column, tick NDI and/or pick a
     "Card out" number for each channel you want played out.
  3. In bridge.json, fill in the "outputs" section with that token. For card
     outputs, set "card" > "device" to the sound card name (or part of it)
     from list-sound-cards.bat, e.g. "Dante Virtual Soundcard" or
     "Focusrite". Leave it "" for the default output. Remove the "card" part
     if you only use NDI outputs.
  NDI outputs appear as "OpenLine <channel>" (with the show name when there
  are several shows).

Sound card links (e.g. a Saramonic WiTalk9 through its Link cable kit)
  A link's "in" and "out" can use "type": "card" with "device" set to the
  sound card name from list-sound-cards.bat, so another headset system joins
  an OpenLine channel. On the WiTalk9 headset, hold ENC for 3 seconds until
  the mic light is solid purple. Full guide: docs/BRIDGE.md, "Sound card links".

If Windows SmartScreen warns about an unknown app, click "More info" >
"Run anyway" (the program isn't code-signed).

To start it automatically with Windows: Task Scheduler > Create Task >
Trigger "At startup" > Action: start run-bridge.bat.

Full guide: docs/BRIDGE.md in the OpenLine repository.
