# Native bootstrap diagnostic coverage

The failure in [Rocky Linux 9 job 109886906846](https://github.com/WuKongIM/WuKongIM/actions/runs/36714723624/job/109886906846) occurred while waiting for package bootstrap and systemd, before explicit service activation. Its empty Docker logs do not establish whether package installation completed. The cause remains unconfirmed.

The launcher now writes fixed update/install/systemd-exec checkpoints to the container log and its private `/run` filesystem. On failure it captures the last checkpoint, PID 1 command, systemd state/jobs, boot journal, service status/journal and Docker log before removing its exact container. Each probe caps emitted output at 64 KiB and uses the existing watchdog with a five-second command deadline (TERM then, if needed, KILL five seconds later). Probe errors cannot replace the original exit code or suppress subsequent cleanup. The 300-second startup and 900-second main validation deadlines are unchanged; bounded failure collection and container removal follow validation.

Failure-first static checkpoints and five shell integration fixtures failed against the original launcher, then passed with this change. The fixtures cover installer/systemd states, probe failure, probe timeout and oversized logs. They are controlled Docker-boundary tests, not evidence of a real Rocky boot. The full native-package static contracts also pass. Raw red/green receipts and frozen instruction digests are retained in the companion evidence archive.

No package publication, failed-run retry or Workflow/policy edit was performed. Real four-distribution lifecycle validation remains owned by automatic source-preview CI.
