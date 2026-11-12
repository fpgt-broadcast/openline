#!/usr/bin/env bash
# Renders preview images to img/ (needs OpenSCAD; on a headless Linux box
# run it under xvfb-run).
set -euo pipefail
cd "$(dirname "$0")"
mkdir -p img
R() { openscad -q --imgsize=1200,1000 --colorscheme=Tomorrow "$@"; }
# --camera = translate x,y,z, rotate x,y,z, distance
R -o img/top.png     --camera=45,33,90,35,0,180,300 beltpack_case.scad
R -o img/angle.png   --camera=45,33,55,65,0,205,400 beltpack_case.scad
R -o img/front.png   --camera=45,33,55,80,0,330,400 beltpack_case.scad
R -o img/back.png    --camera=45,33,55,75,0,150,400 beltpack_case.scad
R -o img/bottom.png  --camera=45,33,30,130,0,20,320 beltpack_case.scad
R -o img/inside.png  --camera=45,33,55,35,0,200,360 -D 'part="body"' beltpack_case.scad
R -o img/layout.png  --camera=45,20,45,70,0,165,330 -D 'part="layout"' beltpack_case.scad
R -o img/side.png    --camera=45,33,50,80,0,80,360 beltpack_case.scad
R -o img/dock.png    --camera=100,45,15,50,0,20,380 -D bays=2 dock.scad
ls -l img
