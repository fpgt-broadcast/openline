// PCB outlines for KiCad (File > Import > Graphics, layer Edge.Cuts).
//   openscad -D 'board="control"' -o control_board_outline.dxf board_outline.scad
//   openscad -D 'board="main"'    -o main_board_outline.dxf    board_outline.scad
// Origin = the board's bottom-left corner. Case positions of parts are in params.scad.
include <params.scad>

board = "control";

if (board == "control") {
    // Lies under the lid, parts on top: OLED header, 4 switches + LEDs, EC11.
    // x/y = the case's coordinates (seen from above, front edge at the bottom) minus cb_x[0], cb_y[0].
    difference() {
        offset(r = 1.5) offset(delta = -1.5) square([cb_x[1] - cb_x[0], cb_y[1] - cb_y[0]]);
        for (p = cb_posts) translate([p[0] - cb_x[0], p[1] - cb_y[0]]) circle(d = 2.2);
    }
} else {
    // Stands upright; seen from the front. Its side edges slide into the case rails.
    offset(r = 1) offset(delta = -1) square([mb_x[1] - mb_x[0], mb_z[1] - mb_z[0]]);
}
