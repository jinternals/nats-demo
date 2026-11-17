import asyncio,sys
L,R=int(sys.argv[1]),int(sys.argv[2])
async def pipe(r,w):
    try:
        while (d:=await r.read(65536)): w.write(d); await w.drain()
    except Exception: pass
    finally: w.close()
async def h(cr,cw):
    rr,rw=await asyncio.open_connection('127.0.0.1',R)
    await asyncio.gather(pipe(cr,rw),pipe(rr,cw))
async def main():
    s=await asyncio.start_server(h,'127.0.0.1',L)
    async with s: await s.serve_forever()
asyncio.run(main())
