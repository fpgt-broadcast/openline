#!/usr/bin/env bash
# Exports every printable part to stl/ and the PCB outlines (needs OpenSCAD 2021+).
set -euo pipefail
cd "$(dirname "$0")"
mkdir -p stl
openscad -q -D 'part="body"'    -o stl/case_body.stl  beltpack_case.scad
openscad -q -D 'part="lid"'     -o stl/case_lid.stl   beltpack_case.scad
openscad -q -D 'part="keycaps"' -o stl/keycaps_x4.stl beltpack_case.scad
openscad -q -D 'part="clip"'    -o stl/belt_clip.stl  beltpack_case.scad
openscad -q -D bays=1 -o stl/dock_1bay.stl dock.scad
openscad -q -D bays=2 -o stl/dock_2bay.stl dock.scad
openscad -q -D 'board="control"' -o control_board_outline.dxf board_outline.scad
openscad -q -D 'board="main"'    -o main_board_outline.dxf    board_outline.scad
openscad -q -o main_board_layout.svg main_board_layout.scad
sed -i "s/stroke-width=\"[0-9.]*\"/stroke-width=\"0.02\"/g; s/fill=\"lightgray\"/fill=\"black\"/g" main_board_layout.svg
ls -l stl *.dxf
