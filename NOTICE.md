# Third-party notices

dcdash is licensed under the [GNU General Public License v3.0 or later](LICENSE). Its binary
embeds the web interface, which bundles works of others. Their licenses are listed in
`web/package.json` and `go.mod` with their dependencies; the works below need attribution
or are notable for other reasons.

## Font Awesome Free

The icons on the buttons are from [Font Awesome Free](https://fontawesome.com) 7 by
Fonticons, Inc.: arrow-rotate-left, arrows-rotate, bolt-lightning, bookmark, display,
key, lock-open, magnifying-glass, pencil, plus, power-off, users (solid), moon and trash-can
(regular).

- Icons: [Creative Commons Attribution 4.0 International](https://creativecommons.org/licenses/by/4.0/)
  (CC BY 4.0). dcdash uses the path data of the icons unchanged, drawn in the current text
  color.
- Code (the npm packages `@fortawesome/free-solid-svg-icons` and
  `@fortawesome/free-regular-svg-icons`): MIT.

See <https://fontawesome.com/license/free>. The Ctrl+Alt+Del icon of the console is dcdash's
own, after the one in Proxmox VE.

## noVNC

The VNC console uses [noVNC](https://github.com/novnc/noVNC) (`@novnc/novnc`), licensed under
the [Mozilla Public License 2.0](https://www.mozilla.org/en-US/MPL/2.0/). Its source is
available from the link above.

## xterm.js

The terminal uses [xterm.js](https://github.com/xtermjs/xterm.js) (`@xterm/xterm`,
`@xterm/addon-fit`), licensed under the MIT license.
