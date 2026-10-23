import pathlib,signal,sys,time
signal.signal(signal.SIGTERM,signal.SIG_IGN);root=pathlib.Path(sys.argv[1]);node=int(sys.argv[2]);(root/f'child-{node}.ready').write_text('ready\n')
while True:time.sleep(1)
