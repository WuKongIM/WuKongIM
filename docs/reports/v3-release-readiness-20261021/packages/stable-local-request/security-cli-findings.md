# security CLI: read-only implementation facts

Only `/usr/bin/security help`, `help add-generic-password`, `-h` and local man
were invoked. No Keychain query or mutation was executed.

- `-i` reads commands from stdin until EOF. Use exactly one command and one
  terminating newline per process: the process returns the last command's
  result, and even a later empty command can replace a failure with zero.
  Do not use verbose mode, which echoes arguments. Input limits are 4096
  characters and 32 arguments. [Apple security.c](https://github.com/apple-oss-distributions/Security/blob/main/SecurityTool/macOS/security.c#L1094-L1137)
- Omitting `-U` uses Create, without the update path; this is the protection
  against overwriting a concurrent existing item. `-X` decodes hexadecimal
  data. For a printable 64-character hex password, send the ASCII password's
  hex encoding (128 characters), then compare returned plaintext bytes.
  [Apple keychain_add.c](https://github.com/apple-oss-distributions/Security/blob/main/SecurityTool/macOS/keychain_add.c#L173-L225)
- `find-generic-password` returns `errSecItemNotFound` (-25300; process status
  44) when the returned item pointer is empty. Its helper can also lose an
  underlying search error, so status 44 alone does not prove absence. Describe
  the probe as “no visible matching item”; fail on additional search-error
  diagnostics and retain the no-`-U` Create protection. No successful existing
  match may be overwritten/read as part of generation. Read back only the exact
  item just created successfully. [Apple find helper](https://github.com/apple-oss-distributions/Security/blob/main/SecurityTool/macOS/keychain_find.c#L127-L142),
  [find result](https://github.com/apple-oss-distributions/Security/blob/main/SecurityTool/macOS/keychain_find.c#L423-L456),
  [error constant](https://github.com/apple-oss-distributions/Security/blob/main/base/SecBase.h#L328)

These are source-level facts, not a successful probe of this Mac's credential
store. They were relayed to the separate operator-script author.
