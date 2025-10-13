# Release Workflow Documentation

## Overview

The GitHub Actions workflow at `.github/workflows/go-ossf-slsa3-publish.yml` has been enhanced to automatically populate GitHub release notes with content from `CHANGELOG.md`.

## How It Works

### Workflow Trigger

The workflow is triggered in two ways:
1. **Automatic**: When a new release is created on GitHub (`release.types: [created]`)
2. **Manual**: Via workflow dispatch from the GitHub Actions UI

### Workflow Jobs

#### 1. Changelog Extraction Job

This job runs first and extracts the relevant changelog content for the release version:

**Steps:**
1. **Checkout**: Checks out the repository code
2. **Extract changelog**: 
   - Identifies the version from the Git tag (e.g., `v2.2.1-20250624`)
   - Searches for the matching version section in `CHANGELOG.md`
   - Extracts all content between the version header and the next version header
   - Removes the version header line and trailing separators
   - Saves the extracted content to `release_notes.md`
3. **Update release**: Updates the GitHub release with the extracted changelog content

**Key Features:**
- ✅ Handles bilingual content (English/Chinese) automatically
- ✅ Preserves all formatting, emojis, and markdown structure
- ✅ Falls back to a default message if version not found in CHANGELOG.md
- ✅ Removes unnecessary separators and trailing whitespace

#### 2. Args Job

Generates build flags (ldflags) for the Go build process. This job depends on the changelog job completing first.

#### 3. Build Job

Builds the Go binaries using the SLSA3 compliant builder for multiple platforms:
- Linux (amd64, arm64)
- Darwin/macOS (amd64, arm64)

#### 4. Verification Job

Verifies the built artifacts using SLSA verifier to ensure supply chain security.

## Usage

### Creating a New Release

#### Option 1: Using GitHub UI (Recommended)

1. **Update CHANGELOG.md**:
   ```bash
   # Make sure your CHANGELOG.md has an entry for the new version
   # Format: ## [vX.Y.Z-YYYYMMDD] - YYYY-MM-DD
   ```

2. **Commit and push changes**:
   ```bash
   git add CHANGELOG.md
   git commit -m "docs: update changelog for vX.Y.Z-YYYYMMDD"
   git push origin main
   ```

3. **Create a new release on GitHub**:
   - Go to: https://github.com/WuKongIM/WuKongIM/releases/new
   - Choose or create a tag (e.g., `v2.2.1-20250624`)
   - Title: `Release v2.2.1-20250624`
   - Description: Leave empty or add a brief summary (will be replaced by changelog)
   - Click "Publish release"

4. **Workflow automatically runs**:
   - The workflow detects the new release
   - Extracts the changelog content for the version
   - Updates the release notes automatically
   - Builds and uploads binaries
   - Verifies the artifacts

#### Option 2: Using Git Tags

1. **Update CHANGELOG.md** (same as above)

2. **Create and push a tag**:
   ```bash
   git tag v2.2.1-20250624
   git push origin v2.2.1-20250624
   ```

3. **Create release from tag**:
   - Go to the tag on GitHub
   - Click "Create release from tag"
   - The workflow will run automatically

#### Option 3: Manual Workflow Dispatch

1. Go to: https://github.com/WuKongIM/WuKongIM/actions/workflows/go-ossf-slsa3-publish.yml
2. Click "Run workflow"
3. Select the branch
4. Click "Run workflow"

**Note**: Manual dispatch requires a release to already exist for the current tag.

## CHANGELOG.md Format Requirements

For the automatic extraction to work correctly, your `CHANGELOG.md` must follow this format:

```markdown
# WuKongIM Changelog

## [v2.2.1-20250624] - 2025-06-24

### 🚀 Major Features
- Feature description
- 功能描述

### 🐛 Bug Fixes
- Bug fix description
- 错误修复描述

---

## [v2.2.0-20250426] - 2025-04-26

### Features
...
```

**Important Rules:**
1. Version headers must start with `## [vX.Y.Z-YYYYMMDD]`
2. The version tag in brackets must match the Git tag exactly
3. Each version section is separated by the next version header
4. The `---` separator is optional and will be removed automatically
5. Both English and Chinese content are preserved

## Troubleshooting

### Release notes not updated

**Possible causes:**
1. Version not found in CHANGELOG.md
   - **Solution**: Ensure the version tag matches exactly (e.g., `v2.2.1-20250624`)
   
2. CHANGELOG.md format incorrect
   - **Solution**: Check that version header follows the format `## [vX.Y.Z-YYYYMMDD]`

3. Workflow permissions issue
   - **Solution**: Ensure the workflow has `contents: write` permission

### Checking workflow logs

1. Go to: https://github.com/WuKongIM/WuKongIM/actions
2. Click on the workflow run
3. Click on the "changelog" job
4. Expand "Extract changelog for version" step
5. Review the output to see what was extracted

### Manual verification

You can test the changelog extraction locally using the test script:

```bash
# Make the test script executable
chmod +x test_changelog_extraction.sh

# Run the test for a specific version
./test_changelog_extraction.sh v2.2.1-20250624

# Check the output
cat release_notes_test.md
```

## Example Output

When the workflow runs successfully, the GitHub release will contain:

```markdown
### 🚀 Major Features

#### Event-Based Messaging System
- **Event Message Support**: Introduced event-based messaging protocol...
- **事件消息支持**: 引入基于事件的消息协议...

### 🆕 New Features

#### API & Documentation
- **OpenAPI Documentation**: Added comprehensive OpenAPI 3.0 specification...
- **OpenAPI文档**: 添加完整的OpenAPI 3.0规范...

[... full changelog content ...]
```

## Benefits

1. **Consistency**: Release notes always match the CHANGELOG.md
2. **Automation**: No manual copy-paste required
3. **Bilingual Support**: Preserves both English and Chinese content
4. **Version Control**: Changelog is version controlled with the code
5. **Transparency**: Clear history of all changes in one place

## Related Files

- `.github/workflows/go-ossf-slsa3-publish.yml` - Main workflow file
- `CHANGELOG.md` - Changelog source
- `test_changelog_extraction.sh` - Local testing script
- `docs/RELEASE_WORKFLOW.md` - This documentation

## Maintenance

### Updating the extraction logic

If you need to modify how changelog content is extracted:

1. Edit the script in `.github/workflows/go-ossf-slsa3-publish.yml`
2. Test locally using `test_changelog_extraction.sh`
3. Commit and push changes
4. Test with a draft release before using in production

### Adding new changelog sections

The extraction script preserves all content between version headers, so you can:
- Add new emoji categories (e.g., 🔒 Security)
- Change section names
- Add subsections
- Include any markdown formatting

All changes will be automatically included in the release notes.

