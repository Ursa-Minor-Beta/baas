# Chrome extensions

Anything you drop in this directory is loaded into the browser when
`CHROME_EXTENSIONS_DIR` points at it. One unpacked extension per subdirectory,
each with a `manifest.json` at its root:

```
profile/Extensions/
  my-extension/
    manifest.json
    background.js
```

No extensions ship with this repository. Redistributing third-party extensions
means redistributing someone else's code under someone else's licence, so
install the ones you want yourself.

Extensions only take effect on the undetected-browser path. The plain headless
path runs with `--disable-extensions`.

To get an unpacked copy of an extension you already have installed, find it
under your Chrome profile's `Extensions/<id>/<version>/` directory and copy that
version directory here.
