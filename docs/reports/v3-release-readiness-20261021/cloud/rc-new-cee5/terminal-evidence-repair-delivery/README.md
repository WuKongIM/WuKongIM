# Bounded public collector-repair evidence

Read reports/terminal-evidence-repair-v2/README.md for the result, limitations and reproduction. V1/V2 first failures and corrected RED remain intact. The source/ overlay is the seven-file reviewed candidate at base cee5d51f325fb84033060f546337d326110adae5. Mac supplementary tests and independent reviews passed; the original Linux 9-minute named check is still required. No passing cloud RC or product-root-cause repair is claimed.

PUBLIC_ALLOWLIST.txt lists every payload. public-manifest.json and SHA256SUMS bind their exact bytes. The deterministic ZIP additionally includes those three control files; archive-sha256.json beside the ZIP archive identifies the archive and checksum list. The private cloud ZIP, raw logs/profiles, SSH configuration and keys are excluded.
