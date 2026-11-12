// OpenLine beltpack, carrier-board prototype — rev D: the "Beltpack 3" mockup on
// the TOP face. Looking down at it on your belt: the 2.42" screen at the far
// edge, the 4 lit keys in a row under it, the volume knob at the end of the row.
// Bottom: XLR-4, USB-C for charging (also the dock) and the DevKit's USB-C for
// flashing. Right side: Ethernet (RJ45). Built-in 18650 across the bottom-back.
// Units mm. Case coordinates: x across (left → right, seen from the FRONT),
// y front (0) → back/belt side (case_d), z bottom (0) → top.
// The wearer looks down from the belt side, so for them x runs right → left.
// Items marked MEASURE depend on the exact parts you bought: check them with
// calipers, change the number here, and re-run build.sh.

$fn = 48;

// ---------- outer case ----------
case_w = 90;      // across
case_d = 66;      // front to back (belt side) — deep enough for the screen + key row on top
body_h = 98;      // body height; the lid sits on top of this
wall   = 2.4;
lid_t  = 2.4;
r_out  = 6;

in_x0 = wall; in_x1 = case_w - wall;
in_y0 = wall; in_y1 = case_d - wall;
in_z0 = wall;                       // inside of the floor
top_z = body_h + lid_t;             // outer top surface

// ---------- top panel ----------
oled_c    = [45, 24];        // MEASURE: centre of the visible area (module centre is assumed; most 2.42" boards are close)
oled_win  = [56, 28.5];      // 2.42" 128×64 SSD1309 active area ≈ 55.0 × 27.5
oled_mod  = [61, 37];        // MEASURE: module board size (sits y 5.5…42.5)
row_y     = 50;              // key row, nearer the wearer ("under" the screen from their view)
// keys 1–4 read left → right for the wearer, so they run right → left in case x
keys      = [for (i = [0:3]) [75 - i * 14.5, row_y]];
key_hole  = 13.6;            // opening; printed key caps are 13.0 square
key_cap   = 13.0;
key_proud = 1.5;             // how far caps stand above the lid
enc_c     = [13.5, row_y];   // EC11 encoder on the control board, shaft up through the lid (wearer's right, end of the row)
enc_hole_d = 7.6;            // M7 bushing

// ---------- control board (under the lid, parts facing up) ----------
cb_gap   = 7.5;     // MEASURE: lid underside → control board top surface.
                    // 6 × 6 × 5 mm tactile switches + an LED under each key cap,
                    // 2.42" OLED on a short header (board top → glass ≈ 6.5).
cb_t     = 1.6;
cb_top_z = body_h - cb_gap;
cb_x     = [in_x0 + 2.6, in_x1 - 2.6];     // board edges, clear of the lid's skirt
cb_y     = [in_y0 + 2.6, in_y1 - 2.6];
cb_posts = [[8, 10], [82, 10], [45, 59]];  // M2 screws hold the board to the lid
cb_post_d = 3.8;
led_off   = [0, 4.6];   // SK6812MINI-E (reverse mount, in a PCB cut-out) offset from each key centre:
                        // beside the 6×6 switch, on the side without legs, still under the hollow cap
oled_stack = 6.5;   // MEASURE: board top → OLED glass top

// ---------- main carrier board (upright, parallel to the front) ----------
mb_y   = 6.0;       // its front surface; modules mount on the back side (+y), ≤ 30 mm deep
mb_t   = 1.6;
mb_z   = [3.0, 84];
rail_w = 3.0;       // side-wall rails with a slot the board slides into
mb_x   = [in_x0 + rail_w - 1.5, in_x1 - rail_w + 1.5];

// ---------- modules on the main board's back (case coords: [x0, x1], [z0, z1], depth y1) ----------
// The main board is laid out around these; every number is MEASURE until the parts arrive.
devkit  = [[6, 32],    [4, 67],  21];   // ESP32-S3-DevKitC-1 in 8.5 mm female header sockets, USB-C ports at the bottom
chg_mod = [[34, 52],   [4.5, 29.5], 15]; // Type-C charge/boost module, 2.5 mm off the board, its USB-C pointing down
codec   = [[35, 60.4], [38, 81], 20];   // SparkFun BOB-21250 WM8960 in header sockets, pin rows vertical
eth_mod = [[62, 87],   [37, 60], 25];   // USR-ES1 W5500, pins soldered in, RJ45 facing the right wall

// ---------- bottom wall: two USB-C + XLR-4 ----------
usb_x     = 18.3;   // MEASURE: DevKit's "USB" port centre across the case (flashing + console)
usb_y     = mb_y + mb_t + 8.5 + 1.6 + 1.6;   // MEASURE: depth of the port centre (header 8.5 + DevKit PCB + half the port)
usb_mouth = 4.0;    // MEASURE: port opening's distance inside the case's bottom surface
usb_cut   = [13.5, 7.5];
chg_x     = 43;     // MEASURE: charge module's USB-C centre across the case (charging, PoE splitter, dock)
chg_y     = mb_y + mb_t + 2.5 + 1.6 + 1.6;   // MEASURE: 2.5 mm spacer + module PCB + half the port
xlr_c     = [66, 25];       // XLR-4 centre on the bottom face (its body stays in front of the battery)
xlr_hole_d = 24;            // MEASURE/verify against the NC4MD-L-1 datasheet (Neutrik D-series)
xlr_screw  = [12, 9.5];     // half-spacing of the 2 diagonal M3 screws (24 × 19 mm pattern)

// ---------- right wall: Ethernet ----------
eth_jack  = [48.5, mb_y + mb_t + 2.5 + 1.6 + 13.3 / 2];   // RJ45 opening centre [z, y]: HR911105A face 16 × 13.3
eth_cut   = [16.8, 14.0];   // [along z, along y]

// ---------- front face ----------
pwr_c    = [72, 78];        // mini slide switch on the main board's front: [x, z]
pwr_slot = [10.0, 4.8];

// ---------- lid screws (horizontal, through the side walls into lid blocks) ----------
lid_skirt  = 7;             // lid skirt depth inside the body
lid_screws = [["left", 16], ["left", 38], ["right", 16], ["right", 38]];
screw_clear = 3.4;
screw_pilot = 2.6;

// ---------- battery: one 18650 lying across, against the back wall, on the floor ----------
batt_in = [in_x1 - in_x0, 22.0];   // holder ≈ 77 × 21 mm; the side walls are its end stops
batt_y  = [in_y1 - batt_in[1], in_y1];
batt_h  = 8;                        // corral wall height (holder + cell ≈ 20 mm tall)

// ---------- belt clip (screwed on at its lower end, above the battery) ----------
clip_holes = [[33, 32], [57, 32]];   // [x, z] on the back face; M3 heat-set inserts
insert_d   = 4.2;
insert_len = 5.0;
clip_w = 44; clip_len = 68; clip_t = 4; clip_spacer = 6;
