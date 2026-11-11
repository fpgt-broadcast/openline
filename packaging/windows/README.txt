OpenLine bridge for Windows
===========================

Connects your venue's NDI audio (program feed, announcer talkback) and vMix
tally to your OpenLine intercom server. It needs no inbound ports or port
forwarding: the bridge connects out to the server.

What's in this folder
  openline-bridge.exe     the bridge
  opus.dll                audio codec (keep it next to the .exe)
  bridge.example.json     example settings
  run-bridge.bat          double-click to start the bridge
  list-ndi-sources.bat    double-click to see NDI sources on the network

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

If Windows SmartScreen warns about an unknown app, click "More info" >
"Run anyway" (the program isn't code-signed).

To start it automatically with Windows: Task Scheduler > Create Task >
Trigger "At startup" > Action: start run-bridge.bat.

Full guide: https://github.com/DueHack12/openline/blob/main/docs/BRIDGE.md
