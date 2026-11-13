// SPDX-License-Identifier: GPL-3.0-or-later

package pvefake

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"

	"github.com/gorilla/websocket"
)

// Size of the fake VNC screen. It is filled with one gray, so the pixel
// format the client asks for does not matter.
const (
	rfbWidth  = 64
	rfbHeight = 48
	rfbName   = "pvefake"
)

// wsReader reads the websocket messages as one byte stream, because RFB
// messages may be split across or combined in websocket messages.
type wsReader struct {
	ws  *websocket.Conn
	buf []byte
}

func (r *wsReader) Read(p []byte) (int, error) {
	for len(r.buf) == 0 {
		_, msg, err := r.ws.ReadMessage()
		if err != nil {
			return 0, err
		}
		r.buf = msg
	}
	n := copy(p, r.buf)
	r.buf = r.buf[n:]
	return n, nil
}

// serveRFB speaks a minimal RFB 3.8 server after the version greeting: no
// authentication, a gray screen in raw encoding for every non-incremental
// update request, and all other client messages are read and ignored.
// Real Proxmox offers VNC authentication with the vncticket as password; the
// fake skips it, so noVNC needs no DES. Returns when the client goes away.
func serveRFB(ws *websocket.Conn) {
	r := &wsReader{ws: ws}
	send := func(b []byte) error { return ws.WriteMessage(websocket.BinaryMessage, b) }
	version := make([]byte, 12)
	if _, err := io.ReadFull(r, version); err != nil {
		return
	}
	if send([]byte{1, 1}) != nil { // one security type: None
		return
	}
	if _, err := io.ReadFull(r, make([]byte, 1)); err != nil {
		return
	}
	if send([]byte{0, 0, 0, 0}) != nil { // SecurityResult OK
		return
	}
	if _, err := io.ReadFull(r, make([]byte, 1)); err != nil { // ClientInit
		return
	}
	var init bytes.Buffer
	_ = binary.Write(&init, binary.BigEndian, []uint16{rfbWidth, rfbHeight})
	// 32 bpp, depth 24, little endian, true color, max 255, shifts 16/8/0.
	init.Write([]byte{32, 24, 0, 1, 0, 255, 0, 255, 0, 255, 16, 8, 0, 0, 0, 0})
	_ = binary.Write(&init, binary.BigEndian, uint32(len(rfbName)))
	init.WriteString(rfbName)
	if send(init.Bytes()) != nil {
		return
	}
	for {
		if err := rfbClientMessage(r, send); err != nil {
			return
		}
	}
}

var errRFB = errors.New("pvefake: unknown RFB client message")

// rfbClientMessage reads one client message and answers update requests.
func rfbClientMessage(r io.Reader, send func([]byte) error) error {
	typ := make([]byte, 1)
	if _, err := io.ReadFull(r, typ); err != nil {
		return err
	}
	skip := func(n int) error { _, err := io.ReadFull(r, make([]byte, n)); return err }
	switch typ[0] {
	case 0: // SetPixelFormat
		return skip(19)
	case 2: // SetEncodings
		hdr := make([]byte, 3)
		if _, err := io.ReadFull(r, hdr); err != nil {
			return err
		}
		return skip(4 * int(binary.BigEndian.Uint16(hdr[1:])))
	case 3: // FramebufferUpdateRequest
		req := make([]byte, 9)
		if _, err := io.ReadFull(r, req); err != nil {
			return err
		}
		if req[0] != 0 { // incremental: nothing changed
			return nil
		}
		var upd bytes.Buffer
		upd.Write([]byte{0, 0, 0, 1}) // FramebufferUpdate, one rectangle
		_ = binary.Write(&upd, binary.BigEndian, []uint16{0, 0, rfbWidth, rfbHeight})
		_ = binary.Write(&upd, binary.BigEndian, int32(0)) // raw encoding
		upd.Write(bytes.Repeat([]byte{0x80}, rfbWidth*rfbHeight*4))
		return send(upd.Bytes())
	case 4: // KeyEvent
		return skip(7)
	case 5: // PointerEvent
		return skip(5)
	case 6: // ClientCutText
		hdr := make([]byte, 7)
		if _, err := io.ReadFull(r, hdr); err != nil {
			return err
		}
		return skip(int(binary.BigEndian.Uint32(hdr[3:])))
	default:
		return errRFB
	}
}
