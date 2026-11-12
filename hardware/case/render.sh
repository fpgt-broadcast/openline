#!/usr/bin/env bash
# Renders preview images to img/ (needs OpenSCAD; on a headless Linux box
# run it under xvfb-run).
set -euo pipefail
cd "$(dirname "$0")"
mkdir -p img
R() { openscad -q --imgsize=1200,1000 --colorscheme=Tomorrow "$@"; }
# --camera = translate x,y,z, rotate x,y,z, distance
R -o img/top.png     --camera=42,25,70,40,0,200,330 beltpack_case.scad
R -o img/front.png   --camera=42,25,60,75,0,330,380 beltpack_case.scad
R -o img/back.png    --camera=42,25,60,75,0,155,380 beltpack_case.scad
R -o img/bottom.png  --camera=42,25,30,130,0,20,300 beltpack_case.scad
R -o img/inside.png  --camera=42,25,60,35,0,210,340 -D 'part="body"' beltpack_case.scad
R -o img/keycaps.png --camera=30,0,3,50,0,20,150 -D 'part="keycaps"' beltpack_case.scad
R -o img/dock.png    --camera=95,40,15,50,0,20,360 -D bays=2 dock.scad
ls -l img
