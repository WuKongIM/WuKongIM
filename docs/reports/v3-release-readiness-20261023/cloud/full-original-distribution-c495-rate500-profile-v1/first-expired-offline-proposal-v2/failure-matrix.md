# v2 pre-copy and owned-process failure matrix

- Preserve v1 bytes. Root initially overlooked its unchanged line22: v1 does compare current inventory with expectedfile; the missing protection is the expectedfile SHA pin, not missing comparison.
- Expectedinventory substitution/contentchange: exactb0685f…b327b1 SHA must match before JSON is trusted.
- Currentoriginal path/file/size/SHA/mtime orsymlink differs: fail before freshOUT mkdir/copy/query.
- Wrong sourceHEAD/tree/dirty clone/sourceDB codec/buildmanifest/CLI SHA orstaleOUT: reject before mkdir.
- Copyfile omitted/extra/size/hash differs from frozenoriginal mapping: never openCLI. Copybudget exactly36files/66,553,406bytes.
- Exactly one expired sample exists (worker2/domain4/gen1/ordinal314/group1646); no other sample or group is in DB scope. Privateidentity source hashes and fingerprint must match.
- Original inventory changes during inspection: markunchangedfalse andfail; originalDB is never opened, onlyPRIVATE0700copies with existingReadOnlyCLI.
- Exactly≤3queries,10s each,limit100,has_morefalse,unique row +clientno fingerprint required. Failed/missing/truncated/nonunique remainsunknown withoutretry or expansion.
- Outerbudget coversguard/hash/copy/query/afterinventory:85s work ceiling,ownedchildPGID TERM≤2s thenKILL/join inside90s total. No globalps scan. If hardstop prevents afterinventory, it staysunknown; do not perform unbounded post-timeout reads.
- Child stdout/stderr are bounded fixedstatus orprivatequeryoutputs; public receipts retainclosedcategories/hash/exit/join only. Pythonuint64 IDs become decimalstrings; UID/payload/SQL remainprivate.
- Static/fake tests use syntheticmaps andowned Python sleeper only, no realDB copy/open/query/product/Go compile/network.
