#!/usr/bin/env python3
"""Generate Helios app icon: abstract flame/torch — bringer of light."""

import math
from PIL import Image, ImageDraw


def draw_helios_icon(size):
    """Draw a flame icon at the given pixel size."""
    img = Image.new('RGBA', (size, size), (0, 0, 0, 0))
    draw = ImageDraw.Draw(img)
    cx, cy = size / 2, size / 2

    # Background
    bg = (18, 12, 30)
    corner = size * 0.18
    draw.rounded_rectangle([0, 0, size - 1, size - 1], radius=corner, fill=bg)

    # Outer flame (orange-red)
    flame_pts = [
        (cx, cy - size * 0.38),          # tip
        (cx + size * 0.08, cy - size * 0.30),
        (cx + size * 0.18, cy - size * 0.15),
        (cx + size * 0.22, cy + size * 0.02),
        (cx + size * 0.20, cy + size * 0.15),
        (cx + size * 0.14, cy + size * 0.25),
        (cx + size * 0.06, cy + size * 0.30),
        (cx, cy + size * 0.32),          # bottom center
        (cx - size * 0.06, cy + size * 0.30),
        (cx - size * 0.14, cy + size * 0.25),
        (cx - size * 0.20, cy + size * 0.15),
        (cx - size * 0.22, cy + size * 0.02),
        (cx - size * 0.18, cy - size * 0.15),
        (cx - size * 0.08, cy - size * 0.30),
    ]
    draw.polygon(flame_pts, fill=(230, 90, 20))

    # Middle flame (orange)
    mid_pts = [
        (cx, cy - size * 0.30),
        (cx + size * 0.06, cy - size * 0.22),
        (cx + size * 0.14, cy - size * 0.08),
        (cx + size * 0.16, cy + size * 0.05),
        (cx + size * 0.12, cy + size * 0.16),
        (cx + size * 0.05, cy + size * 0.24),
        (cx, cy + size * 0.26),
        (cx - size * 0.05, cy + size * 0.24),
        (cx - size * 0.12, cy + size * 0.16),
        (cx - size * 0.16, cy + size * 0.05),
        (cx - size * 0.14, cy - size * 0.08),
        (cx - size * 0.06, cy - size * 0.22),
    ]
    draw.polygon(mid_pts, fill=(245, 150, 30))

    # Inner flame (yellow)
    inner_pts = [
        (cx, cy - size * 0.20),
        (cx + size * 0.04, cy - size * 0.14),
        (cx + size * 0.09, cy - size * 0.02),
        (cx + size * 0.10, cy + size * 0.08),
        (cx + size * 0.06, cy + size * 0.16),
        (cx, cy + size * 0.20),
        (cx - size * 0.06, cy + size * 0.16),
        (cx - size * 0.10, cy + size * 0.08),
        (cx - size * 0.09, cy - size * 0.02),
        (cx - size * 0.04, cy - size * 0.14),
    ]
    draw.polygon(inner_pts, fill=(255, 210, 60))

    # Core (bright white-yellow)
    core_pts = [
        (cx, cy - size * 0.10),
        (cx + size * 0.04, cy + size * 0.02),
        (cx + size * 0.03, cy + size * 0.10),
        (cx, cy + size * 0.13),
        (cx - size * 0.03, cy + size * 0.10),
        (cx - size * 0.04, cy + size * 0.02),
    ]
    draw.polygon(core_pts, fill=(255, 245, 180))

    return img


def main():
    import os

    base = os.path.dirname(os.path.abspath(__file__))

    # Generate master icon at 1024px
    master = draw_helios_icon(1024)
    bg_color = (18, 12, 30)

    # Android mipmap sizes
    android_sizes = {
        'mipmap-mdpi': 48,
        'mipmap-hdpi': 72,
        'mipmap-xhdpi': 96,
        'mipmap-xxhdpi': 144,
        'mipmap-xxxhdpi': 192,
    }

    for folder, px in android_sizes.items():
        path = os.path.join(base, 'android', 'app', 'src', 'main', 'res', folder, 'ic_launcher.png')
        resized = master.resize((px, px), Image.LANCZOS)
        resized.save(path)
        print(f"  Android {folder}: {path} ({px}x{px})")

    # iOS icon sizes
    ios_sizes = {
        'Icon-App-20x20@1x.png': 20,
        'Icon-App-20x20@2x.png': 40,
        'Icon-App-20x20@3x.png': 60,
        'Icon-App-29x29@1x.png': 29,
        'Icon-App-29x29@2x.png': 58,
        'Icon-App-29x29@3x.png': 87,
        'Icon-App-40x40@1x.png': 40,
        'Icon-App-40x40@2x.png': 80,
        'Icon-App-40x40@3x.png': 120,
        'Icon-App-60x60@2x.png': 120,
        'Icon-App-60x60@3x.png': 180,
        'Icon-App-76x76@1x.png': 76,
        'Icon-App-76x76@2x.png': 152,
        'Icon-App-83.5x83.5@2x.png': 167,
        'Icon-App-1024x1024@1x.png': 1024,
    }

    ios_dir = os.path.join(base, 'ios', 'Runner', 'Assets.xcassets', 'AppIcon.appiconset')
    for filename, px in ios_sizes.items():
        path = os.path.join(ios_dir, filename)
        resized = master.resize((px, px), Image.LANCZOS)
        # iOS icons must be RGB (no alpha)
        rgb = Image.new('RGB', (px, px), bg_color)
        rgb.paste(resized, mask=resized.split()[3])
        rgb.save(path)
        print(f"  iOS {filename}: {path} ({px}x{px})")

    # Flutter web icons
    web_dir = os.path.join(base, 'web')
    if os.path.isdir(web_dir):
        for name, px in [('favicon.png', 16), ('icons/Icon-192.png', 192),
                         ('icons/Icon-512.png', 512), ('icons/Icon-maskable-192.png', 192),
                         ('icons/Icon-maskable-512.png', 512)]:
            path = os.path.join(web_dir, name)
            if os.path.exists(path):
                resized = master.resize((px, px), Image.LANCZOS)
                rgb = Image.new('RGB', (px, px), bg_color)
                rgb.paste(resized, mask=resized.split()[3])
                rgb.save(path)
                print(f"  Web {name}: ({px}x{px})")

    print("\nDone! All flame icons generated.")


if __name__ == '__main__':
    main()
