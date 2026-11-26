import fcntl
import os
import re
import signal
import struct
import sys
import termios
import tty


def send(text):
    os.write(1, text.encode())


phase = None
typed = False
control = False
pasted = False
clicked = False
resize_count = 0


def redraw():
    send(f"\x1b[2J\x1b[Hchild={os.getpid()} 雪")
    send("\x1b[2;3Hcursor-ok")
    if typed:
        send(f"\x1b[3;1Hinput-ok-{phase}")
    if control:
        send(f"\x1b[7;1Hcontrol-ok-{phase}")
    if pasted:
        send(f"\x1b[4;1Hpaste-ok-{phase}")
    if clicked:
        send(f"\x1b[5;1Hmouse-ok-{phase}")
    if phase is not None and resize_count:
        rows, cols, _, _ = struct.unpack("HHHH", fcntl.ioctl(0, termios.TIOCGWINSZ, b"\0" * 8))
        send(f"\x1b[6;1Hresize-ok-{phase} {cols}x{rows}")


def on_resize(signum, frame):
    global resize_count
    if phase is not None:
        resize_count += 1
    redraw()


tty.setraw(0)
signal.signal(signal.SIGWINCH, on_resize)
send("\x1b[?1049h\x1b[?2004h\x1b[?1000h\x1b[?1006h\x1b[2J\x1b[H")
send(f"child={os.getpid()} 雪\x1b[2;3H\x1b[6n")
reply = bytearray()
while not reply.endswith(b"R"):
    reply.extend(os.read(0, 1))
if reply != b"\x1b[2;3R":
    raise RuntimeError(f"unexpected detached cursor reply {reply!r}")
send("cursor-ok")
with open(sys.argv[1], "w") as ready:
    ready.write(str(os.getpid()))
history = bytearray()
while True:
    history.extend(os.read(0, 256))
    match = re.search("typed-雪-([12])[\\r\\n]".encode(), history)
    if match:
        phase = match[1].decode()
        typed = True
        control = pasted = clicked = False
        resize_count = 0
        del history[:match.end()]
    control |= b"\x03" in history
    pasted |= b"\x1b[200~paste\x1b[201~" in history
    clicked |= bool(re.search(rb"\x1b\[<0;\d+;\d+M", history))
    if control and pasted and clicked:
        history.clear()
    redraw()
