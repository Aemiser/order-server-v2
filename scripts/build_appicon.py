#!/usr/bin/env python3
"""从 build/icon-src 下的候选原图生成 OnePoint 应用图标。

产出：build/appicon.png（Wails 主图标，1024 RGBA 透明底）
      build/windows/icon.ico（Windows exe / 任务栏图标，多尺寸）
      build/icon-src/sizes-<variant>/（16~512 各尺寸 PNG，供 favicon 等复用）

用法：python scripts/build_appicon.py [a|b]
  a = Q 版包裹精灵（更极简）   b = AI 小机器人 + 订单清单（默认）
"""

import argparse
import shutil
from pathlib import Path

import numpy as np
from PIL import Image, ImageDraw

ROOT = Path(__file__).resolve().parent.parent
SRC = ROOT / "build" / "icon-src"
BUILD = ROOT / "build"
WINDOWS = BUILD / "windows"

CANVAS = 1024
PAD = 120                 # 四周留白，统一两个候选的构图
SIDE = CANVAS - 2 * PAD   # 784
RADIUS = 170              # 圆角半径
ICO_SIZES = [(256, 256), (128, 128), (64, 64), (48, 48), (32, 32), (16, 16)]


def candidate(variant: str) -> Path:
    folder = SRC / f"anime-{variant}"
    files = sorted(folder.glob("*.png"))
    if not files:
        raise SystemExit(f"找不到候选原图：{folder}")
    return files[0]


def make_master(path: Path) -> Image.Image:
    im = Image.open(path).convert("RGB")
    if im.size != (CANVAS, CANVAS):
        im = im.resize((CANVAS, CANVAS), Image.Resampling.LANCZOS)

    arr = np.array(im)
    # 裁切区四角必须落在深色底板内，避免把白底/水印带进图标
    corners = [arr[PAD, PAD], arr[PAD, -PAD - 1], arr[-PAD - 1, PAD], arr[-PAD - 1, -PAD - 1]]
    if any(int(c.min()) > 200 for c in corners):
        raise SystemExit("裁切区四角含有浅色背景，请调大 PAD 后重试")

    crop = im.crop((PAD, PAD, PAD + SIDE, PAD + SIDE))

    # 4 倍超采样画圆角蒙版，缩小后得到平滑的抗锯齿边缘
    scale = 4
    mask = Image.new("L", (CANVAS * scale, CANVAS * scale), 0)
    ImageDraw.Draw(mask).rounded_rectangle(
        (PAD * scale, PAD * scale, (PAD + SIDE) * scale, (PAD + SIDE) * scale),
        radius=RADIUS * scale,
        fill=255,
    )
    mask = mask.resize((CANVAS, CANVAS), Image.Resampling.LANCZOS)

    out = Image.new("RGBA", (CANVAS, CANVAS), (0, 0, 0, 0))
    out.paste(crop, (PAD, PAD))
    out.putalpha(mask)
    return out


def backup_originals() -> None:
    dst = SRC / "original-backup"
    dst.mkdir(parents=True, exist_ok=True)
    for name, src in (("appicon-wails-default.png", BUILD / "appicon.png"),
                      ("icon-wails-default.ico", WINDOWS / "icon.ico")):
        target = dst / name
        if src.exists() and not target.exists():
            shutil.copy2(src, target)


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("variant", nargs="?", default="b", choices=["a", "b"])
    args = ap.parse_args()

    src = candidate(args.variant)
    backup_originals()
    master = make_master(src)

    preview = SRC / f"onepoint-icon-{args.variant}.png"
    master.save(preview)
    master.save(BUILD / "appicon.png")
    master.save(WINDOWS / "icon.ico", format="ICO", sizes=ICO_SIZES)

    sizes = SRC / f"sizes-{args.variant}"
    sizes.mkdir(parents=True, exist_ok=True)
    for size in (512, 256, 128, 64, 48, 32, 16):
        master.resize((size, size), Image.Resampling.LANCZOS).save(sizes / f"appicon-{size}.png")

    print(f"variant      : {args.variant}")
    print(f"source       : {src}")
    print(f"preview      : {preview}")
    print(f"appicon.png  : {BUILD / 'appicon.png'}  ({master.size[0]}x{master.size[1]} RGBA)")
    print(f"icon.ico     : {WINDOWS / 'icon.ico'}  {[s[0] for s in ICO_SIZES]}")
    print(f"sizes        : {sizes}")


if __name__ == "__main__":
    main()
