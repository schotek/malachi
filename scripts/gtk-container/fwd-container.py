#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 Vladislav Janeček
# SPDX-License-Identifier: GPL-3.0-or-later
# Container side of the GTK live test: listens on a unix socket PATH and
# forwards to HOST:PORT (the Mac through the container bridge), where
# fwd-host.py hands the bytes to the daemon's socket.
# Usage: fwd-container.py PATH HOST PORT
import asyncio, os, sys
path, host, port = sys.argv[1], sys.argv[2], int(sys.argv[3])
async def pipe(r, w):
    try:
        while True:
            d = await r.read(65536)
            if not d: break
            w.write(d); await w.drain()
    except Exception: pass
    finally:
        try: w.close()
        except Exception: pass
async def handle(cr, cw):
    try:
        tr, tw = await asyncio.open_connection(host, port)
    except Exception:
        cw.close(); return
    await asyncio.gather(pipe(cr, tw), pipe(tr, cw))
async def main():
    try: os.unlink(path)
    except FileNotFoundError: pass
    srv = await asyncio.start_unix_server(handle, path)
    os.chmod(path, 0o600)
    print(f"forwarding {path} -> {host}:{port}", flush=True)
    async with srv: await srv.serve_forever()
asyncio.run(main())
