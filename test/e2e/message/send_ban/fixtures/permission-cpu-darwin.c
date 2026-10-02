// Harness-only bounded snapshots of at most three explicitly owned node PIDs.
// Preserve raw Mach CPU units and process identity; conversion happens on deltas.
#include <errno.h>
#include <inttypes.h>
#include <limits.h>
#include <libproc.h>
#include <mach/mach_time.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <sys/resource.h>
#include <time.h>

static uint64_t monotonic_ns(void) {
    struct timespec ts;
    if (clock_gettime(CLOCK_MONOTONIC, &ts) != 0) exit(3);
    return (uint64_t)ts.tv_sec * UINT64_C(1000000000) + (uint64_t)ts.tv_nsec;
}

int main(int argc, char **argv) {
    if (argc < 2 || argc > 4) return 2;
    int pids[3];
    struct rusage_info_v2 samples[3] = {0};
    uint64_t sampled_at[3];
    mach_timebase_info_data_t timebase;
    if (mach_timebase_info(&timebase) != KERN_SUCCESS || !timebase.numer || !timebase.denom) return 3;
    for (int i = 1; i < argc; i++) {
        char *end;
        errno = 0;
        long pid = strtol(argv[i], &end, 10);
        if (errno || *end || end == argv[i] || pid <= 0 || pid > INT_MAX) return 2;
        pids[i-1] = (int)pid;
        for (int j = 0; j < i-1; j++) if (pids[j] == pid) return 2;
    }
    uint64_t begin = monotonic_ns();
    for (int i = 0; i < argc-1; i++) {
        if (proc_pid_rusage(pids[i], RUSAGE_INFO_V2, (rusage_info_t *)&samples[i]) != 0) return 3;
        sampled_at[i] = monotonic_ns();
    }
    uint64_t finish = monotonic_ns();
    printf("{\"timebase_numer\":%u,\"timebase_denom\":%u,\"started_monotonic_ns\":%" PRIu64 ",\"finished_monotonic_ns\":%" PRIu64 ",\"processes\":[", timebase.numer, timebase.denom, begin, finish);
    for (int i = 0; i < argc-1; i++) {
        printf("%s{\"pid\":%d,\"start_abstime\":%" PRIu64 ",\"user_ticks\":%" PRIu64 ",\"system_ticks\":%" PRIu64 ",\"sampled_monotonic_ns\":%" PRIu64 "}", i ? "," : "", pids[i], samples[i].ri_proc_start_abstime, samples[i].ri_user_time, samples[i].ri_system_time, sampled_at[i]);
    }
    puts("]}");
    return 0;
}
