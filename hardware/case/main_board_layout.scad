// Main board placement drawing, seen from the MODULE side (the back of the case,
// so left/right are mirrored compared with the front view). Origin = the board's
// bottom-left corner from this side; bottom edge = the case's bottom.
//   openscad -o main_board_layout.svg main_board_layout.scad
// Rectangles are the module footprints from params.scad (MEASURE them before the
// PCB is drawn); they are where the header sockets / pins go, not copper.
include <params.scad>

bw = mb_x[1] - mb_x[0];
bh = mb_z[1] - mb_z[0];
function u(x) = mb_x[1] - x;      // mirror: module side
function v(z) = z - mb_z[0];

module outline(w = 0.4) { difference() { offset(w / 2) children(); offset(-w / 2) children(); } }
module box(m, label, sub = "") {
    x0 = u(m[0][1]); x1 = u(m[0][0]); z0 = v(m[1][0]); z1 = v(m[1][1]);
    translate([x0, z0]) outline() square([x1 - x0, z1 - z0]);
    translate([(x0 + x1) / 2, (z0 + z1) / 2 + (sub == "" ? 0 : 2)]) text(label, size = 2.6, halign = "center", valign = "center", font = "Liberation Sans:style=Bold");
    if (sub != "") translate([(x0 + x1) / 2, (z0 + z1) / 2 - 2.4]) text(sub, size = 1.8, halign = "center", valign = "center", font = "Liberation Sans");
}
module arrow_down(x, y, label) {
    translate([x, y]) { polygon([[-1.6, 0], [1.6, 0], [0, -2.6]]); translate([0, 1.6]) text(label, size = 1.8, halign = "center", font = "Liberation Sans"); }
}

outline(0.6) square([bw, bh]);
box(devkit, "ESP32-S3", "DevKitC-1, sockets");
box(chg_mod, "CHARGE", "on 2.5 mm pins");
box(codec, "WM8960", "BOB-21250, sockets");
box(eth_mod, "W5500", "USR-ES1");
translate([u(eth_mod[0][1]) + 1.2, v(eth_jack[0]) - 6]) text("< RJ45 to wall", size = 1.6, font = "Liberation Sans");
// ports and openings
arrow_down(u(usb_x), 2.6, "USB");
arrow_down(u(chg_x), 2.6, "CHG");
translate([u(xlr_c[0]) - 11, 0.8]) outline() square([22, 26]);
translate([u(xlr_c[0]), 14]) text("XLR body", size = 2, halign = "center", font = "Liberation Sans");
// other side of the board
translate([u(pwr_c[0]) - 4.3, v(pwr_c[1]) - 2]) outline(0.3) square([8.6, 4]);
translate([u(pwr_c[0]), v(pwr_c[1]) - 4.6]) text("power switch", size = 1.5, halign = "center", font = "Liberation Sans");
translate([u(pwr_c[0]), v(pwr_c[1]) - 6.8]) text("(other side)", size = 1.5, halign = "center", font = "Liberation Sans");
translate([bw / 2, bh - 2.5]) text("top edge: 12-pin cable to the control board", size = 1.8, halign = "center", font = "Liberation Sans");
translate([bw / 2, -6]) text(str("Main board ", bw, " × ", bh, " mm, seen from the module side"), size = 2.4, halign = "center", font = "Liberation Sans:style=Bold");
