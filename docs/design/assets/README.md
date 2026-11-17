# Preview assets

The desktop uses bundled Space Grotesk Regular and IBM Plex Mono Regular,
matching the typography reference supplied by the user. Chinese text uses the
system font fallback. Font files were retrieved from the iroh website's public
font assets on November 16, 2026:

- `fonts/SpaceGrotesk.ttf`: https://www.iroh.computer/fonts/space-grotesk.ttf
  — SIL OFL in `fonts/SpaceGrotesk-OFL.txt`, from
  https://github.com/floriankarsten/space-grotesk.
- `fonts/IBMPlexMono.ttf`: https://www.iroh.computer/fonts/ibm-plex-mono.ttf
  — SIL OFL in `fonts/IBMPlex-OFL.txt`, from https://github.com/IBM/plex.

Both fonts are embedded without changes and require no runtime network access.

Geist and Geist Mono variable WOFF2 fonts are distributed with their SIL Open
Font License in `fonts/OFL.txt`. These earlier assets are retained unchanged.

Source: https://github.com/vercel/geist-font

Files: `fonts/Geist/webfonts/Geist[wght].woff2` and
`fonts/GeistMono/webfonts/GeistMono[wght].woff2`, retrieved November 16, 2026.

The SVG symbols in `desktop-review.fragment.html` are authored for Linkport:
socket-and-cable logo, local workstation, remote machine, serial socket,
Bluetooth port, network link and interface actions. The logo is a graphic,
without lettering; the desktop and transparent menu bar assets use the same
two-socket form. Function icons use a 24-unit grid, 1.5-unit rounded stroke and
common optical size. No external icon runtime is needed.
