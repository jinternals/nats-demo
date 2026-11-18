import asyncio,sys
async def pipe(r,w):
    try:
        while (d:=await r.read(65536)): w.write(d); await w.drain()
    except Exception: pass
    finally: w.close()
async def h(cr,cw):
    line=(await cr.readline()).decode()
    while (await cr.readline()) not in (b"\r\n",b""): pass
    meth,target,_=line.split()
    print("PROXY",meth,target,flush=True)
    if meth!="CONNECT": cw.write(b"HTTP/1.1 405 Method Not Allowed\r\n\r\n"); cw.close(); return
    host,port=target.rsplit(":",1)
    rr,rw=await asyncio.open_connection(host,int(port))
    cw.write(b"HTTP/1.1 200 Connection established\r\n\r\n"); await cw.drain()
    await asyncio.gather(pipe(cr,rw),pipe(rr,cw))
async def main():
    s=await asyncio.start_server(h,'127.0.0.1',3128)
    async with s: await s.serve_forever()
asyncio.run(main())
