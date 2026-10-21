#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>
#include <unistd.h>
#include <sys/types.h>
#include <sys/wait.h>
#include <fcntl.h>
static double now(void) { struct timespec t; clock_gettime(CLOCK_MONOTONIC,&t); return t.tv_sec+t.tv_nsec/1e9; }
int main(int argc,char **argv) {
 const char *tool=strrchr(argv[0],'/'); tool=tool?tool+1:argv[0]; char key[128]; snprintf(key,sizeof(key),"PROFILE_REAL_%s",tool); const char *real=getenv(key); const char *log=getenv("PROFILE_PHASE_LOG"); if (!real || !log) return 91;
 double start=now(); pid_t parent=getppid(); pid_t child=fork(); if (child<0) return 92; if (!child) { execv(real,argv); _exit(93); }
 int status; if (waitpid(child,&status,0)<0) return 94; double end=now(); int result=WIFEXITED(status)?WEXITSTATUS(status):128+WTERMSIG(status); char line[320]; int len=snprintf(line,sizeof(line),"{\"tool\":\"%s\",\"pid\":%d,\"ppid\":%d,\"start\":%.9f,\"end\":%.9f,\"duration\":%.9f,\"exit\":%d}\n",tool,getpid(),parent,start,end,end-start,result);
 int fd=open(log,O_WRONLY|O_CREAT|O_APPEND,0600); if (fd>=0) { write(fd,line,len); close(fd); } return result;
}
