// OpenLine beltpack, carrier-board prototype (rev B: controls on the top edge).
// Units mm. Case coordinates: x across (left → right, seen from the front),
// y front (0) → back/belt side (case_d), z bottom (0) → top.
// Items marked MEASURE depend on the exact parts you bought: check them with
// calipers, change the number here, and re-run build.sh.

$fn = 48;

// ---------- outer case ----------
case_w = 84;      // across
case_d = 50;      // front to back (belt side)
body_h = 118;     // body height; the lid sits on top of this
wall   = 2.4;
lid_t  = 2.4;
r_out  = 6;

in_x0 = wall; in_x1 = case_w - wall;
in_y0 = wall; in_y1 = case_d - wall;
in_z0 = wall;                       // inside of the floor
top_z = body_h + lid_t;             // outer top surface

// ---------- top panel: screen + 2×2 lit keys ----------
// Laid out for the WEARER, who looks down from the belt side: from there the
// screen is on the left, keys read 1 2 / 3 4 on the right, knob on the right
// side wall. (Seen from the front, x runs the other way.) Mount the OLED so
// its text reads from the belt side; if it's upside down, turn the module.
oled_c    = [61.5, 25];      // centre of the display window
oled_win  = [32, 18];        // 1.3" SH1106 active area ≈ 29.4 × 14.7
oled_mod  = [35.4, 33.5];    // MEASURE: OLED module board size
keys      = [[32, 16], [14, 16], [32, 34], [14, 34]];   // keys 1 2 (far row), 3 4 (near row), wearer's view
key_hole  = 13.6;            // opening; printed key caps are 13.0 square
key_cap   = 13.0;
key_proud = 1.5;             // how far caps stand above the lid

// ---------- control board (under the lid, parts facing up) ----------
cb_gap   = 7.5;     // MEASURE: lid underside → control board top surface.
                    // 6 × 6 × 5 mm tactile switches + LED under each key cap.
cb_t     = 1.6;
cb_top_z = body_h - cb_gap;
cb_x     = [in_x0 + 2.6, in_x1 - 2.6];     // board edges, clear of the lid's skirt
cb_y     = [in_y0 + 2.6, in_y1 - 2.6];
cb_posts = [[41.6, 9.5], [41.6, 40.5], [23, 25]];   // M2 screws hold the board to the lid
cb_post_d = 3.8;
oled_stack = 5.4;   // MEASURE: board top → OLED glass top (header + module + glass)

// ---------- main carrier board (upright, parallel to the front) ----------
mb_y   = 6.0;       // its front surface; modules mount on the back side (+y)
mb_t   = 1.6;
mb_z   = [3.0, 104];
rail_w = 3.0;       // side-wall rails with a slot the board slides into
mb_x   = [in_x0 + rail_w - 1.5, in_x1 - rail_w + 1.5];

// ---------- bottom wall: USB-C + XLR-4 ----------
usb_x     = 18.3;   // MEASURE: DevKit USB-C centre across the case (DevKit on the left seen from the front, USB at its bottom edge)
usb_y     = mb_y + mb_t + 8.5 + 1.6 + 1.6;   // MEASURE: depth of the port centre (header 8.5 + DevKit PCB + half the port)
usb_mouth = 4.0;    // MEASURE: port opening's distance inside the case's bottom surface
usb_cut   = [13.5, 7.5];
xlr_c     = [58, 25];       // XLR-4 centre on the bottom face
xlr_hole_d = 24;            // MEASURE/verify against the NC4MD-L-1 datasheet (Neutrik D-series)
xlr_screw  = [12, 9.5];     // half-spacing of the 2 diagonal M3 screws (24 × 19 mm pattern)

// ---------- side walls ----------
enc_c   = [25, 96];         // EC11 encoder (M7 bushing) through the x = 0 wall (wearer's right): [y, z]
enc_hole_d = 7.3;
pwr_c   = [69, 100];        // mini slide switch through the front face: [x, z]
pwr_slot = [10.0, 4.8];

// ---------- lid screws (horizontal, through the walls into lid blocks) ----------
lid_skirt  = 7;             // lid skirt depth inside the body
lid_screws = [["front", 24], ["front", 60], ["back", 24], ["back", 60]];
screw_clear = 3.4;
screw_pilot = 2.6;

// ---------- battery: one 18650 standing upright behind the main board ----------
batt_x = [57.2, 79];        // holder footprint across
batt_y = [25.4, 47.2];      // and front-to-back (against the back wall)
batt_z = [28, 106];         // MEASURE: holder height ~77 mm

// ---------- belt clip (screwed on at its lower end) ----------
clip_holes = [[30, 20], [54, 20]];   // [x, z] on the back face; M3 heat-set inserts
insert_d   = 4.2;
insert_len = 5.0;
clip_w = 44; clip_len = 88; clip_t = 4; clip_spacer = 6;
