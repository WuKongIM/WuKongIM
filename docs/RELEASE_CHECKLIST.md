# Release Checklist

Quick reference guide for creating a new release with automatic changelog integration.

## Pre-Release Checklist

- [ ] All changes merged to main branch
- [ ] All tests passing
- [ ] Version number decided (format: `vX.Y.Z-YYYYMMDD`)
- [ ] CHANGELOG.md updated with new version entry

## Step-by-Step Release Process

### 1. Update CHANGELOG.md

```bash
# Edit CHANGELOG.md and add new version section at the top
# Format:
## [v2.2.1-20250624] - 2025-06-24

### 🚀 Major Features
...

### 🐛 Bug Fixes
...
```

**Important**: The version in brackets `[v2.2.1-20250624]` must match your Git tag exactly!

### 2. Commit and Push Changes

```bash
git add CHANGELOG.md
git commit -m "docs: update changelog for v2.2.1-20250624"
git push origin main
```

### 3. Create GitHub Release

#### Option A: Via GitHub UI (Recommended)

1. Go to: https://github.com/WuKongIM/WuKongIM/releases/new
2. Click "Choose a tag"
3. Type the new version: `v2.2.1-20250624`
4. Click "Create new tag: v2.2.1-20250624 on publish"
5. Release title: `Release v2.2.1-20250624`
6. Description: Leave empty (will be auto-filled from CHANGELOG.md)
7. Click "Publish release"

#### Option B: Via Git Command Line

```bash
# Create and push tag
git tag v2.2.1-20250624
git push origin v2.2.1-20250624

# Then create release from tag on GitHub UI
```

### 4. Monitor Workflow

1. Go to: https://github.com/WuKongIM/WuKongIM/actions
2. Watch the "SLSA Go releaser" workflow
3. Verify all jobs complete successfully:
   - ✅ changelog
   - ✅ args
   - ✅ build (4 matrix jobs)
   - ✅ verification

### 5. Verify Release

1. Go to: https://github.com/WuKongIM/WuKongIM/releases
2. Click on the new release
3. Verify:
   - [ ] Release notes populated from CHANGELOG.md
   - [ ] Bilingual content (English/Chinese) preserved
   - [ ] All binaries uploaded (4 files + provenance)
   - [ ] SLSA provenance files present

## Post-Release Checklist

- [ ] Release notes look correct
- [ ] All binaries downloadable
- [ ] Docker images published (separate workflow)
- [ ] Announcement prepared (if needed)
- [ ] Documentation updated (if needed)

## Troubleshooting

### Release notes are empty or incorrect

**Check:**
1. Version tag matches CHANGELOG.md exactly
2. CHANGELOG.md format is correct: `## [vX.Y.Z-YYYYMMDD]`
3. Workflow logs for errors

**Fix:**
```bash
# Test extraction locally
./test_changelog_extraction.sh v2.2.1-20250624

# If successful, manually update release
gh release edit v2.2.1-20250624 --notes-file release_notes_test.md
```

### Workflow fails

**Common issues:**
1. **Permission denied**: Check repository settings → Actions → Workflow permissions
2. **Tag already exists**: Delete tag and recreate
3. **Build fails**: Check Go version compatibility

**View logs:**
```bash
# Using GitHub CLI
gh run list --workflow=go-ossf-slsa3-publish.yml
gh run view <run-id> --log
```

### Need to update release notes after publishing

```bash
# Edit CHANGELOG.md
git add CHANGELOG.md
git commit -m "docs: fix changelog for v2.2.1-20250624"
git push

# Extract and update manually
./test_changelog_extraction.sh v2.2.1-20250624
gh release edit v2.2.1-20250624 --notes-file release_notes_test.md
```

## Quick Commands Reference

```bash
# Test changelog extraction locally
./test_changelog_extraction.sh v2.2.1-20250624

# Create and push tag
git tag v2.2.1-20250624
git push origin v2.2.1-20250624

# Delete tag (if needed)
git tag -d v2.2.1-20250624
git push origin :refs/tags/v2.2.1-20250624

# View releases
gh release list

# View specific release
gh release view v2.2.1-20250624

# Edit release notes manually
gh release edit v2.2.1-20250624 --notes "New content"

# Edit release notes from file
gh release edit v2.2.1-20250624 --notes-file release_notes.md

# Download release assets
gh release download v2.2.1-20250624

# View workflow runs
gh run list --workflow=go-ossf-slsa3-publish.yml

# Watch workflow run
gh run watch
```

## Version Naming Convention

Format: `vMAJOR.MINOR.PATCH-YYYYMMDD`

Examples:
- `v2.2.1-20250624` - Version 2.2.1 released on June 24, 2025
- `v2.3.0-20250701` - Version 2.3.0 released on July 1, 2025
- `v3.0.0-20260101` - Version 3.0.0 released on January 1, 2026

## CHANGELOG.md Template

```markdown
## [vX.Y.Z-YYYYMMDD] - YYYY-MM-DD

### 🚀 Major Features

#### Feature Category
- **Feature Name**: Description in English
- **功能名称**: 中文描述

### 🆕 New Features

#### Category
- **Feature**: Description
- **功能**: 描述

### 🐛 Bug Fixes

#### Category
- **Fix**: Description
- **修复**: 描述

### 🔧 Technical Improvements

#### Category
- **Improvement**: Description
- **改进**: 描述

### 📚 Documentation

- **Docs**: Description
- **文档**: 描述

### 🔄 Breaking Changes

- **Change**: Description
- **变更**: 描述

---
```

## Resources

- [Release Workflow Documentation](./RELEASE_WORKFLOW.md)
- [GitHub Releases Guide](https://docs.github.com/en/repositories/releasing-projects-on-github)
- [SLSA Framework](https://slsa.dev/)
- [GitHub CLI Documentation](https://cli.github.com/manual/)

## Support

If you encounter issues:
1. Check workflow logs in GitHub Actions
2. Test locally with `test_changelog_extraction.sh`
3. Review [RELEASE_WORKFLOW.md](./RELEASE_WORKFLOW.md) for detailed troubleshooting
4. Open an issue if the problem persists

