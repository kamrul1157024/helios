#!/usr/bin/env python3
"""Draw the Play Store icon, feature graphic and phone screenshots.

Usage: python3 scripts/play_graphics.py   (needs Pillow: pip install pillow)

Writes into docs/play-store/:
  icon-512.png         the app icon from mobile/generate_icon.py, full-bleed
  feature-graphic.png  1024x500, in the colours of docs/assets/social-preview
  screenshots/         the README's in-app phone shots, padded to 2:1
"""

import sys
from pathlib import Path

from PIL import Image, ImageDraw, ImageFont

ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / "docs" / "play-store"
SHOTS = ROOT / "docs" / "assets" / "mobile"
BOLD = "/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf"
REGULAR = "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf"

sys.path.insert(0, str(ROOT / "mobile"))
from generate_icon import BG, draw_helios_icon  # noqa: E402

# In the order the listing shows them. The notification shots are left out:
# they show a home screen full of other companies' app icons, which Play's
# metadata policy does not allow in a listing.
SCREENSHOTS = [
    "sessions.png",
    "session-detail.png",
    "terminal.png",
    "files.png",
    "git.png",
    "permission-mode.png",
]


def icon():
    # Play rounds the corners itself, so the icon it is given is square. The
    # app icon is drawn larger and its rounded corners cropped away.
    big = draw_helios_icon(576)
    big.crop((32, 32, 544, 544)).save(OUT / "icon-512.png")


def feature_graphic():
    w, h = 1024, 500
    g = Image.new("RGB", (w, h), BG)
    # The warm light off the flame, as on the social preview.
    px = g.load()
    gx, gy, radius = 180, 120, 620
    for y in range(h):
        for x in range(w):
            d = ((x - gx) ** 2 + (y - gy) ** 2) ** 0.5 / radius
            t = max(0.0, 1 - d) ** 2 * 0.22
            px[x, y] = tuple(int(BG[i] + ((245, 150, 30)[i] - BG[i]) * t) for i in range(3))

    flame = draw_helios_icon(240)
    g.paste(flame, (90, 130), flame)

    d = ImageDraw.Draw(g)
    d.text((370, 140), "helios", font=ImageFont.truetype(BOLD, 96), fill=(243, 240, 248))
    d.text(
        (374, 270),
        "Your coding agents, in your pocket.",
        font=ImageFont.truetype(REGULAR, 32),
        fill=(243, 240, 248),
    )
    d.text(
        (376, 325),
        "Watch, answer and approve Claude Code",
        font=ImageFont.truetype(REGULAR, 24),
        fill=(167, 159, 189),
    )
    d.text(
        (376, 358),
        "and Codex sessions from anywhere.",
        font=ImageFont.truetype(REGULAR, 24),
        fill=(167, 159, 189),
    )
    g.save(OUT / "feature-graphic.png")


def screenshots():
    # The phone shots are taller than 2:1, which Play refuses. Each is padded
    # left and right with its own edge colour rather than cropped, so nothing
    # on the screen is lost.
    out = OUT / "screenshots"
    out.mkdir(parents=True, exist_ok=True)
    for old in out.glob("*.png"):
        old.unlink()
    for i, name in enumerate(SCREENSHOTS, start=1):
        shot = Image.open(SHOTS / name).convert("RGB")
        w, h = shot.size
        width = max(w, (h + 1) // 2)
        edge = shot.getpixel((0, h // 2))
        padded = Image.new("RGB", (width, h), edge)
        padded.paste(shot, ((width - w) // 2, 0))
        padded.save(out / f"{i:02d}-{Path(name).stem}.png")


def main():
    OUT.mkdir(parents=True, exist_ok=True)
    icon()
    feature_graphic()
    screenshots()
    print("wrote", OUT)


if __name__ == "__main__":
    main()
