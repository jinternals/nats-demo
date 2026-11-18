import asyncio,sys
# usage: wanproxy.py LOCAL:REMOTE [LOCAL:REMOTE ...]  (all on 127.0.0.1)
async def pipe(r,w):
    try:
        while (d:=await r.read(65536)): w.write(d); await w.drain()
    except Exception: pass
    finally: w.close()
def handler(R):
    async def h(cr,cw):
        try: rr,rw=await asyncio.open_connection('127.0.0.1',R)
        except OSError: cw.close(); return
        await asyncio.gather(pipe(cr,rw),pipe(rr,cw))
    return h
async def main():
    srv=[await asyncio.start_server(handler(int(p.split(':')[1])),'127.0.0.1',int(p.split(':')[0])) for p in sys.argv[1:]]
    await asyncio.gather(*(s.serve_forever() for s in srv))
asyncio.run(main())
