# Native package bootstrap diagnostics

Approved scope: distinguish installer, PID 1 and systemd boot failures without changing acceptance thresholds, retrying an existing CI run or changing Workflow/policy control. Source: `424d03eb298b972ec572261d617972eecb5523c5`.

Failure-first cases: incomplete installation, systemd still initializing, a failing diagnostic command, a blocked diagnostic command and oversized output. The existing failure must remain nonzero and exact owned-container cleanup must still occur. Shell integration fixtures exercise the real launcher through a controlled Docker boundary; static checkpoints cover update/install/exec ordering. They do not prove a real distribution boot.

Observed failure: Rocky job 109886906846 in run 36714723624 stopped after 300 seconds with empty container logs, before explicit service activation. Underlying cause remains unknown. Preserve that failed run. New-source CI may validate this diagnostic change; it cannot retroactively classify the old failure.
