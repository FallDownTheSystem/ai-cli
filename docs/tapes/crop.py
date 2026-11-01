"""Crop recorded GIFs to the area the terminal text actually used.

VHS records the whole terminal, but most answers stop well short of the
right and bottom edges. The crop box is the union of every frame's content,
so nothing that ever appears on screen is cut off. The right and bottom
margins match the tape's padding, so the frame looks even on all sides.

Usage: python docs/tapes/crop.py docs/*.gif
Needs Pillow and ffmpeg.
"""

import os
import subprocess
import sys

from PIL import Image, ImageChops, ImageSequence

PADDING = 24
THRESHOLD = 24


def content_box(path):
	im = Image.open(path)
	width, height = im.size
	background = None
	box = None
	for frame in ImageSequence.Iterator(im):
		rgb = frame.convert("RGB")
		if background is None:
			background = Image.new("RGB", (width, height), rgb.getpixel((1, 1)))
		mask = ImageChops.difference(rgb, background).convert("L").point(lambda v: 255 if v > THRESHOLD else 0)
		b = mask.getbbox()
		if b:
			box = b if box is None else (min(box[0], b[0]), min(box[1], b[1]), max(box[2], b[2]), max(box[3], b[3]))
	return (width, height), box


def crop(path):
	(width, height), box = content_box(path)
	if box is None:
		print(f"{path}: empty, skipped")
		return
	# Even sizes keep scaled renders from blurring by half a pixel.
	w = min(width, box[2] + PADDING)
	h = min(height, box[3] + PADDING)
	w -= w % 2
	h -= h % 2
	if (w, h) == (width, height):
		print(f"{path}: {width}x{height}, already tight")
		return
	tmp = path + ".tmp.gif"
	subprocess.run(
		[
			"ffmpeg", "-v", "error", "-y", "-i", path,
			"-filter_complex", f"crop={w}:{h}:0:0,split[a][b];[a]palettegen=stats_mode=full[p];[b][p]paletteuse=dither=none",
			"-loop", "0", tmp,
		],
		check=True,
	)
	os.replace(tmp, path)
	print(f"{path}: {width}x{height} -> {w}x{h}")


if __name__ == "__main__":
	for arg in sys.argv[1:]:
		crop(arg)
