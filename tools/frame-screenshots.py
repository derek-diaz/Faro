"""Create self-contained SVG frames for the documentation's PNG screenshots.

Run after replacing the PNG captures: python tools/frame-screenshots.py
"""

import base64
import struct
from pathlib import Path


SCREENSHOTS = Path(__file__).resolve().parents[1] / "docs" / "screenshots"
RADIUS = 24


def main():
    for name in ("dashboard", "activity", "devices"):
        source = SCREENSHOTS / f"{name}.png"
        png = source.read_bytes()
        if png[:8] != b"\x89PNG\r\n\x1a\n":
            raise ValueError(f"Not a PNG screenshot: {source}")
        width, height = struct.unpack(">II", png[16:24])
        encoded = base64.b64encode(png).decode("ascii")
        svg = f'''<svg xmlns="http://www.w3.org/2000/svg" width="{width}" height="{height}" viewBox="0 0 {width} {height}">
  <title>Faro {name} with example data</title>
  <defs>
    <clipPath id="frame">
      <rect width="{width}" height="{height}" rx="{RADIUS}" />
    </clipPath>
  </defs>
  <image width="{width}" height="{height}" clip-path="url(#frame)" href="data:image/png;base64,{encoded}" />
</svg>
'''
        source.with_suffix(".svg").write_text(svg, encoding="utf-8")
        print(f"Framed {name}: {width} x {height}, radius {RADIUS}px")


if __name__ == "__main__":
    main()
