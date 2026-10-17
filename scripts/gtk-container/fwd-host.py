#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 Vladislav Janeček
# SPDX-License-Identifier: GPL-3.0-or-later
# Host side of the GTK live test: forwards TCP 127.0.0.1:PORT and the
# container bridge address 192.168.64.1:PORT to the daemon's unix socket, so
# that the GTK client in the container reaches a daemon running on the Mac.
# Usage: fwd-host.py PORT /path/to/rpc.sock
import asyncio, sys
port, path = int(sys.argv[1]), sys.argv[2]
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
        ur, uw = await asyncio.open_unix_connection(path)
    except Exception as e:
        cw.close(); return
    await asyncio.gather(pipe(cr, uw), pipe(ur, cw))
async def main():
    srv = await asyncio.start_server(handle, ['127.0.0.1', '192.168.64.1'], port)
    print(f"forwarding 127.0.0.1:{port} -> {path}", flush=True)
    async with srv: await srv.serve_forever()
asyncio.run(main())
