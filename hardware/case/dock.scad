// OpenLine charging dock (rev D case). The beltpack stands upright and its
// charging USB-C port ("CHG", the charge module's) drops onto a USB-C plug held in the dock floor.
// Unplug the headset before docking (the XLR is on the bottom too).
//   openscad -D bays=1 -o dock_1bay.stl dock.scad
//   openscad -D bays=2 -o dock_2bay.stl dock.scad   (fits a 220 mm bed)
//
// Plug: any USB-C cable. Push the plug up through the slot from underneath
// until the flat face of its plastic body stands `usb_mouth` mm above the
// pocket floor, hot-glue it from below, and lay the cable in the groove.
include <params.scad>

bays          = 1;
pocket_depth  = 16;            // the belt clip starts above this, so no notch is needed
pocket_clear  = 0.6;
plug_body     = [12.6, 7.0];   // MEASURE: plug overmould width × thickness
plug_body_len = 16;            // MEASURE: overmould length
side          = 9;
cable_d       = 5;

floor_h = plug_body_len - usb_mouth + 3;
base_h  = floor_h + pocket_depth;
pitch   = case_w + 2 * pocket_clear + side;
dock_w  = bays * pitch + side;
dock_d  = case_d + 2 * pocket_clear + 2 * side;

module rrect(size, r) { offset(r) square([size[0] - 2 * r, size[1] - 2 * r], center = true); }
// footprint of the bottom of the case (+ clearance), in case x/y coordinates
module case_foot(grow) { translate([case_w / 2, case_d / 2]) rrect([case_w + 2 * grow, case_d + 2 * grow], r_out + grow); }

difference() {
    hull() for (x = [6, dock_w - 6], y = [6, dock_d - 6]) {
        translate([x, y, 0]) cylinder(r = 6, h = base_h - 4);
        translate([x, y, base_h - 4]) scale([1, 1, 0.66]) sphere(r = 6);
    }
    for (i = [0 : bays - 1]) translate([side + pocket_clear + i * pitch, side + pocket_clear, 0]) {
        translate([0, 0, floor_h]) linear_extrude(pocket_depth + 1) case_foot(pocket_clear);
        translate([0, 0, base_h - 2.5]) hull() {       // lead-in chamfer
            linear_extrude(0.01) case_foot(pocket_clear);
            translate([0, 0, 2.6]) linear_extrude(0.01) case_foot(pocket_clear + 2.5);
        }
        translate([chg_x, chg_y, -1]) linear_extrude(floor_h + 2) rrect(plug_body, 2);
        translate([chg_x - cable_d / 2, chg_y, -0.01]) cube([cable_d, dock_d, cable_d]);
        translate([case_w / 2, -side - pocket_clear + 0.6, floor_h / 2 + 2]) rotate([90, 0, 0]) linear_extrude(1)
            text(str("OPENLINE ", i + 1), size = 4.5, halign = "center", valign = "center", font = "Liberation Sans:style=Bold");
    }
    for (x = [12, dock_w - 12], y = [12, dock_d - 12]) translate([x, y, -0.01]) cylinder(d = 11, h = 1.2);
}
