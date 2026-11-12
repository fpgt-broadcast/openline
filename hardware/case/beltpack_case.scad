// OpenLine beltpack case, rev D — the "Beltpack 3" mockup on the TOP face:
// big 2.42" screen, 4 lit keys in a row under it, volume knob at the end of the row.
// Parts:  body | lid | keycaps | clip | assembly     (see build.sh)
//  body    — print standing up, open top up. Bottom has XLR-4 + USB-C.
//  lid     — print upside down (top face on the bed).
//  keycaps — 4 caps; print top-down in translucent PETG/PLA so the LEDs glow through.
//  clip    — print flat, PETG.
include <params.scad>

part = "assembly";

// ---------- helpers ----------
module rbox(p0, p1, r) {
    hull() for (x = [p0[0] + r, p1[0] - r], y = [p0[1] + r, p1[1] - r], z = [p0[2] + r, p1[2] - r])
        translate([x, y, z]) sphere(r, $fn = 32);
}
module rrect(size, r) { offset(r) square([size[0] - 2 * r, size[1] - 2 * r], center = true); }
module outer_shape() { rbox([0, 0, 0], [case_w, case_d, top_z], r_out); }
module inner_2d() { translate([in_x0, in_y0]) offset(r_out - wall) offset(-(r_out - wall)) square([in_x1 - in_x0, in_y1 - in_y0]); }

// Each lid screw: [point on the inner wall surface, inward normal]
function lid_screw_pos(s) =
    s[0] == "front" ? [[s[1], in_y0, body_h - 3.5], [0, 1, 0]] :
    s[0] == "back"  ? [[s[1], in_y1, body_h - 3.5], [0, -1, 0]] :
    s[0] == "left"  ? [[in_x0, s[1], body_h - 3.5], [1, 0, 0]] :
                      [[in_x1, s[1], body_h - 3.5], [-1, 0, 0]];
// rotate +z to point along axis vector v
module look(v) { if (v[1] != 0) rotate([v[1] > 0 ? -90 : 90, 0, 0]) children(); else rotate([0, v[0] > 0 ? 90 : -90, 0]) children(); }

// ---------- body ----------
module body() {
    difference() {
        union() {
            difference() {
                outer_shape();
                rbox([in_x0, in_y0, in_z0], [in_x1, in_y1, top_z + 20], r_out - wall);
                translate([-50, -50, body_h]) cube([300, 300, 100]);
            }
            // rails the main board slides into (one per side wall)
            for (x0 = [in_x0 - 0.1, in_x1 - rail_w])
                translate([x0, in_y0 - 0.1, in_z0 - 0.1]) cube([rail_w + 0.1, mb_y + mb_t + 2.4 - in_y0, mb_z[1] - in_z0]);
            // battery corral: a low wall in front of the holder (side walls are the end stops)
            translate([in_x0 - 0.1, batt_y[0] - 1.6, in_z0 - 0.1]) cube([in_x1 - in_x0 + 0.2, 1.6, batt_h + 0.1]);
            // belt-clip insert bosses on the back wall
            for (c = clip_holes) translate([c[0], in_y1 + 0.1, c[1]]) rotate([90, 0, 0]) cylinder(d = 9, h = insert_len + 2 - wall + 0.1);
        }
        // main-board slots (board edges sit 1.5 mm into each rail)
        for (x0 = [in_x0 + rail_w - 1.5 - 0.2, in_x1 - rail_w - 0.1])
            translate([x0, mb_y - 0.15, in_z0]) cube([1.8, mb_t + 0.3, mb_z[1]]);
        // bottom: USB-C (sized for the plug's overmould) and XLR-4 with its two screws
        translate([usb_x, usb_y, -1]) linear_extrude(wall + 2) rrect(usb_cut, 2.5);
        translate([xlr_c[0], xlr_c[1], -1]) {
            cylinder(d = xlr_hole_d, h = wall + 2);
            for (s = [[-xlr_screw[0], xlr_screw[1]], [xlr_screw[0], -xlr_screw[1]]]) translate(s) cylinder(d = 3.2, h = wall + 2);
        }
        // front face: power switch slot (switch is soldered to the main board's front)
        translate([pwr_c[0], -1, pwr_c[1]]) rotate([-90, 0, 0]) linear_extrude(wall + 2) rrect([pwr_slot[0], pwr_slot[1]], 1.2);
        // lid screws
        for (s = lid_screws) let(p = lid_screw_pos(s)) translate(p[0] - p[1] * (wall + 1)) look(p[1]) cylinder(d = screw_clear, h = wall + 2);
        // belt-clip heat-set inserts, pressed in from outside
        for (c = clip_holes) translate([c[0], case_d + 1, c[1]]) rotate([90, 0, 0]) cylinder(d = insert_d, h = insert_len + 1);
        // grip grooves on both sides
        for (z = [26 : 8 : 82], x = [-0.01, case_w - 1]) translate([x, 10, z]) cube([1.01, case_d - 20, 2.2]);
        // name on the front face
        translate([case_w / 2, 0.6, 44]) rotate([90, 0, 0]) linear_extrude(1)
            text("OPENLINE", size = 6, halign = "center", valign = "center", spacing = 1.2, font = "Liberation Sans:style=Bold");
    }
}

// ---------- lid ----------
module key_row_box(grow) {
    xs = [for (k = keys) k[0]];
    translate([(min(xs) + max(xs)) / 2, row_y]) rrect([max(xs) - min(xs) + key_hole + grow, key_hole + grow], 3);
}
module lid() {
    difference() {
        union() {
            intersection() { outer_shape(); translate([-50, -50, body_h]) cube([300, 300, 100]); }
            // skirt that drops inside the body
            translate([0, 0, body_h - lid_skirt]) linear_extrude(lid_skirt + 0.01) difference() {
                offset(-0.35) inner_2d(); offset(-0.35 - 1.6) inner_2d();
            }
            // blocks the side screws thread into (4.7 deep from the wall, 10 wide, full skirt height)
            for (s = lid_screws) let(p = lid_screw_pos(s))
                translate([p[0][0], p[0][1], body_h - lid_skirt / 2] + p[1] * (0.35 + 4.7 / 2))
                    cube(p[1][0] != 0 ? [4.7, 10, lid_skirt + 0.02] : [10, 4.7, lid_skirt + 0.02], center = true);
            // posts holding the control board (M2 from below)
            for (p = cb_posts) translate([p[0], p[1], cb_top_z]) cylinder(d = cb_post_d, h = cb_gap + 0.1);
            // frame pressing lightly on the OLED glass around the window
            translate([oled_c[0], oled_c[1], cb_top_z + oled_stack + 0.2]) linear_extrude(body_h - cb_top_z - oled_stack) difference() {
                rrect(oled_win + [3, 3], 1.5); rrect(oled_win, 1);
            }
            // raised bezel around the display, and the guard frame around the key row
            translate([oled_c[0], oled_c[1], top_z - 0.01]) linear_extrude(1.2) difference() {
                rrect(oled_win + [7, 6], 3); rrect(oled_win + [1.5, 1.5], 1.5);
            }
            translate([0, 0, top_z - 0.01]) linear_extrude(1.6) difference() {
                key_row_box(4);
                for (k = keys) translate(k) rrect([key_hole + 0.8, key_hole + 0.8], 2);
            }
        }
        for (p = cb_posts) translate([p[0], p[1], cb_top_z - 1]) cylinder(d = 1.7, h = cb_gap);
        for (s = lid_screws) let(p = lid_screw_pos(s)) translate(p[0] - p[1] * 0.5) look(p[1]) cylinder(d = screw_pilot, h = 8);
        // display window, chamfered on the outside
        translate([oled_c[0], oled_c[1], body_h - 1]) hull() {
            linear_extrude(0.01) rrect(oled_win, 1);
            translate([0, 0, lid_t + 3]) linear_extrude(0.01) rrect(oled_win + [2.5, 2.5], 1.5);
        }
        for (k = keys) translate([k[0], k[1], body_h - 1]) linear_extrude(lid_t + 5) rrect([key_hole, key_hole], 1.5);
        translate([enc_c[0], enc_c[1], body_h - 1]) cylinder(d = enc_hole_d, h = lid_t + 5);
    }
}

// ---------- lit key caps (plunger presses a 6×6 tactile switch; LED sits beside it under the cap) ----------
cap_tab = [1.0, 6.0];   // retaining tabs on the two x sides (keep the cap captive under the lid)
cap_h = 1.2 + lid_t + key_proud;               // flange + lid + standing proud
stem_ext = (body_h - 1.2) - (cb_top_z + 5.0);   // reach down to a 5 mm tall switch's plunger
module keycap(n) {
    // modelled top-down for printing: z = 0 is the cap's top face
    difference() {
        union() {
            linear_extrude(cap_h) rrect([key_cap, key_cap], 1.5);
            translate([0, 0, cap_h - 1.2]) linear_extrude(1.2) square([key_cap + 2 * cap_tab[0], cap_tab[1]], center = true);
            cylinder(d = 3.2, h = cap_h + stem_ext);
        }
        translate([0, 0, 1.6]) linear_extrude(cap_h) difference() {   // hollow so the LED lights the cap
            rrect([key_cap - 2, key_cap - 2], 1); circle(d = 3.2);
        }
        // number reads upright for the wearer (looking down from the belt side)
        translate([0, 0, -0.01]) linear_extrude(0.6) rotate(180)
            text(str(n), size = 5.5, halign = "center", valign = "center", font = "Liberation Sans:style=Bold");
    }
}

// ---------- belt clip (lower end screws to the case; open end at the top) ----------
module rrect_corner(size, r) { translate(size / 2) rrect(size, r); }
clip_mount_len = 16;
module belt_clip() {
    span = clip_holes[1][0] - clip_holes[0][0];
    difference() {
        union() {
            linear_extrude(clip_t) rrect_corner([clip_w, clip_len], 6);
            linear_extrude(clip_t + clip_spacer) rrect_corner([clip_w, clip_mount_len], 4);
            translate([2, clip_len - 4, 0]) rotate([0, 90, 0]) translate([-clip_t, 0, 0]) cylinder(d = 5, h = clip_w - 4);
        }
        for (dx = [-span / 2, span / 2]) translate([clip_w / 2 + dx, clip_mount_len / 2, -1]) {
            cylinder(d = 3.4, h = 30);
            cylinder(d1 = 6.6, d2 = 3.4, h = 1 + 1.6);
        }
        translate([clip_w / 2, clip_len + 2, -1]) cylinder(d = 14, h = clip_t + 2);   // finger notch
    }
}

// ---------- output ----------
if (part == "body") body();
else if (part == "lid") translate([0, case_d, top_z + 1.6]) rotate([180, 0, 0]) lid();   // top face down
else if (part == "keycaps") for (i = [0:3]) translate([i * 20, 0, 0]) keycap(i + 1);
else if (part == "clip") belt_clip();
else {
    color("#363b42") body();
    color("#2a2e34") translate([0, 0, 0.3]) lid();
    for (i = [0:3]) color("#d8e4ee", 0.85) translate([keys[i][0], keys[i][1], top_z + key_proud + 0.3]) mirror([0, 0, 1]) keycap(i + 1);
    color("#1d2024") translate([enc_c[0], enc_c[1], top_z + 0.8]) cylinder(d = 16, h = 14);   // knob (bought)
    color("#0e1013") translate([oled_c[0], oled_c[1], cb_top_z + oled_stack - 0.6]) linear_extrude(0.6) rrect(oled_win + [2, 2], 1);  // screen
    color("#c9a640") translate([cb_x[0], cb_y[0], cb_top_z - cb_t]) cube([cb_x[1] - cb_x[0], cb_y[1] - cb_y[0], cb_t]);
    color("#c9a640") translate([mb_x[0], mb_y, mb_z[0]]) cube([mb_x[1] - mb_x[0], mb_t, mb_z[1] - mb_z[0]]);
    color("#4a90d9", 0.8) translate([case_w / 2, (batt_y[0] + batt_y[1]) / 2, in_z0 + 10]) rotate([0, 90, 0]) cylinder(d = 18.5, h = 70, center = true);
    cx = (clip_holes[0][0] + clip_holes[1][0]) / 2;
    color("#50565e") translate([cx - clip_w / 2, case_d + clip_t + clip_spacer, clip_holes[0][1] - clip_mount_len / 2])
        rotate([90, 0, 0]) belt_clip();
}
