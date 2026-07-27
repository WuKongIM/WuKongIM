package backup

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/WuKongIM/WuKongIM/cmd/wkcli/internal/command"
	backupkeys "github.com/WuKongIM/WuKongIM/pkg/backup/keypackage"
	"github.com/spf13/cobra"
)

const (
	deploymentRecoveryKitFileName = "wukongim-backup-recovery.wkr"
	deploymentRecoveryKeyFileName = "wukongim-backup-recovery.key"
	maxKeyCommandPackageBytes     = 64 << 10
	maxKeyCommandRecoveryKitBytes = 96 << 10
	maxKeyCommandRecoveryKeyBytes = 256
)

func newKeysCommand(deps command.Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "keys",
		Short: "Bootstrap and maintain the protected deployment key package",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(
		newKeysBootstrapCommand(deps),
		newKeysInspectCommand(deps),
		newKeysRotateCommand(deps),
		newKeysRecoverCommand(deps),
	)
	return cmd
}

func newKeysBootstrapCommand(deps command.Deps) *cobra.Command {
	var repositoryID string
	var outputDirectory string
	cmd := &cobra.Command{
		Use:   "bootstrap",
		Short: "Create one runtime package and offline recovery kit",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			repositoryID = strings.TrimSpace(repositoryID)
			outputDirectory = strings.TrimSpace(outputDirectory)
			if repositoryID == "" || outputDirectory == "" {
				return keyCommandExit(
					"--repository-id and --out-dir are required",
				)
			}
			packageBody, metadata, err :=
				backupkeys.GenerateDeploymentKeyPackage(repositoryID)
			if err != nil {
				return keyCommandExit(err.Error())
			}
			defer zeroKeyCommandBytes(packageBody)
			kitBody, recoveryKey, kitMetadata, err :=
				backupkeys.SealDeploymentRecoveryKit(packageBody)
			if err != nil {
				return keyCommandExit(err.Error())
			}
			defer zeroKeyCommandBytes(kitBody)
			defer zeroKeyCommandBytes(recoveryKey)
			if metadata != kitMetadata {
				return keyCommandExit(
					"generated recovery metadata does not match the package",
				)
			}
			recoveryKeyBody := []byte(
				base64.StdEncoding.EncodeToString(recoveryKey) + "\n",
			)
			defer zeroKeyCommandBytes(recoveryKeyBody)
			if err := writeKeyBootstrapDirectory(
				outputDirectory, packageBody, kitBody, recoveryKeyBody,
			); err != nil {
				return keyCommandExit(err.Error())
			}
			return writeKeyMetadata(deps.Stdout, metadata)
		},
	}
	cmd.Flags().StringVar(
		&repositoryID, "repository-id", "",
		"Stable backup repository identity bound into every envelope",
	)
	cmd.Flags().StringVar(
		&outputDirectory, "out-dir", "",
		"New private directory for generated key artifacts",
	)
	return cmd
}

func newKeysInspectCommand(deps command.Deps) *cobra.Command {
	var packagePath string
	cmd := &cobra.Command{
		Use:   "inspect",
		Short: "Validate a package and print only non-secret metadata",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			packagePath = strings.TrimSpace(packagePath)
			if packagePath == "" {
				return keyCommandExit("--package is required")
			}
			body, err := readPrivateKeyCommandFile(
				packagePath, maxKeyCommandPackageBytes,
			)
			if err != nil {
				return keyCommandExit(err.Error())
			}
			defer zeroKeyCommandBytes(body)
			metadata, err :=
				backupkeys.InspectDeploymentKeyPackage(body)
			if err != nil {
				return keyCommandExit(err.Error())
			}
			return writeKeyMetadata(deps.Stdout, metadata)
		},
	}
	cmd.Flags().StringVar(
		&packagePath, "package", "",
		"Protected deployment key package path",
	)
	return cmd
}

func newKeysRotateCommand(deps command.Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rotate",
		Short: "Stage and activate a rolling key rotation",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(
		newKeysRotationPhaseCommand(deps, "stage"),
		newKeysRotationPhaseCommand(deps, "activate"),
	)
	return cmd
}

func newKeysRotationPhaseCommand(
	deps command.Deps,
	phase string,
) *cobra.Command {
	var packagePath string
	var recoveryKeyPath string
	var outputDirectory string
	cmd := &cobra.Command{
		Use:   phase,
		Short: phase + " one deployment key rotation revision",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			packagePath = strings.TrimSpace(packagePath)
			recoveryKeyPath = strings.TrimSpace(recoveryKeyPath)
			outputDirectory = strings.TrimSpace(outputDirectory)
			if packagePath == "" ||
				recoveryKeyPath == "" ||
				outputDirectory == "" {
				return keyCommandExit(
					"--package, --recovery-key, and --out-dir are required",
				)
			}
			packageBody, err := readPrivateKeyCommandFile(
				packagePath, maxKeyCommandPackageBytes,
			)
			if err != nil {
				return keyCommandExit(err.Error())
			}
			defer zeroKeyCommandBytes(packageBody)
			recoveryKey, err := readRecoveryKeyFile(recoveryKeyPath)
			if err != nil {
				return keyCommandExit(err.Error())
			}
			defer zeroKeyCommandBytes(recoveryKey)
			var rotated []byte
			var metadata backupkeys.DeploymentKeyPackageMetadata
			switch phase {
			case "stage":
				rotated, metadata, err =
					backupkeys.StageDeploymentKeyRotation(packageBody)
			case "activate":
				rotated, metadata, err =
					backupkeys.ActivateDeploymentKeyRotation(packageBody)
			default:
				err = fmt.Errorf("unsupported key rotation phase")
			}
			if err != nil {
				return keyCommandExit(err.Error())
			}
			defer zeroKeyCommandBytes(rotated)
			kitBody, kitMetadata, err :=
				backupkeys.RefreshDeploymentRecoveryKit(
					rotated, recoveryKey,
				)
			if err != nil {
				return keyCommandExit(err.Error())
			}
			defer zeroKeyCommandBytes(kitBody)
			if metadata != kitMetadata {
				return keyCommandExit(
					"rotated recovery metadata does not match the package",
				)
			}
			if err := writeKeyRotationDirectory(
				outputDirectory, rotated, kitBody,
			); err != nil {
				return keyCommandExit(err.Error())
			}
			return writeKeyMetadata(deps.Stdout, metadata)
		},
	}
	cmd.Flags().StringVar(
		&packagePath, "package", "",
		"Current protected deployment key package path",
	)
	cmd.Flags().StringVar(
		&recoveryKeyPath, "recovery-key", "",
		"Offline recovery-key file created by bootstrap",
	)
	cmd.Flags().StringVar(
		&outputDirectory, "out-dir", "",
		"New private directory for the rotated package and recovery kit",
	)
	return cmd
}

func newKeysRecoverCommand(deps command.Deps) *cobra.Command {
	var kitPath string
	var recoveryKeyPath string
	var outputPath string
	cmd := &cobra.Command{
		Use:   "recover",
		Short: "Restore a runtime package from its offline recovery kit",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			kitPath = strings.TrimSpace(kitPath)
			recoveryKeyPath = strings.TrimSpace(recoveryKeyPath)
			outputPath = strings.TrimSpace(outputPath)
			if kitPath == "" ||
				recoveryKeyPath == "" ||
				outputPath == "" {
				return keyCommandExit(
					"--recovery-kit, --recovery-key, and --out are required",
				)
			}
			kitBody, err := readPrivateKeyCommandFile(
				kitPath, maxKeyCommandRecoveryKitBytes,
			)
			if err != nil {
				return keyCommandExit(err.Error())
			}
			defer zeroKeyCommandBytes(kitBody)
			recoveryKey, err := readRecoveryKeyFile(recoveryKeyPath)
			if err != nil {
				return keyCommandExit(err.Error())
			}
			defer zeroKeyCommandBytes(recoveryKey)
			packageBody, metadata, err :=
				backupkeys.OpenDeploymentRecoveryKit(
					kitBody, recoveryKey,
				)
			if err != nil {
				return keyCommandExit(err.Error())
			}
			defer zeroKeyCommandBytes(packageBody)
			if err := writePrivateKeyCommandFile(
				outputPath, packageBody,
			); err != nil {
				return keyCommandExit(err.Error())
			}
			return writeKeyMetadata(deps.Stdout, metadata)
		},
	}
	cmd.Flags().StringVar(
		&kitPath, "recovery-kit", "",
		"Encrypted offline recovery-kit path",
	)
	cmd.Flags().StringVar(
		&recoveryKeyPath, "recovery-key", "",
		"Offline recovery-key path",
	)
	cmd.Flags().StringVar(
		&outputPath, "out", "",
		"New protected runtime package path",
	)
	return cmd
}

func writeKeyBootstrapDirectory(
	directory string,
	packageBody []byte,
	kitBody []byte,
	recoveryKeyBody []byte,
) error {
	files := []struct {
		name string
		body []byte
	}{
		{name: backupkeys.DeploymentKeyPackageCredentialName, body: packageBody},
		{name: deploymentRecoveryKitFileName, body: kitBody},
		{name: deploymentRecoveryKeyFileName, body: recoveryKeyBody},
	}
	return writePrivateKeyCommandDirectory(directory, files)
}

func writeKeyRotationDirectory(
	directory string,
	packageBody []byte,
	kitBody []byte,
) error {
	return writePrivateKeyCommandDirectory(directory, []struct {
		name string
		body []byte
	}{
		{name: backupkeys.DeploymentKeyPackageCredentialName, body: packageBody},
		{name: deploymentRecoveryKitFileName, body: kitBody},
	})
}

// writePrivateKeyCommandDirectory creates one new private directory and rolls
// back every file if the complete artifact set cannot be persisted.
func writePrivateKeyCommandDirectory(
	directory string,
	files []struct {
		name string
		body []byte
	},
) error {
	if err := os.Mkdir(directory, 0o700); err != nil {
		if os.IsExist(err) {
			return fmt.Errorf(
				"backup keys: output directory already exists",
			)
		}
		return fmt.Errorf("backup keys: create output directory: %w", err)
	}
	created := make([]string, 0, len(files))
	cleanup := func() {
		for _, path := range created {
			_ = os.Remove(path)
		}
		_ = os.Remove(directory)
	}
	for _, file := range files {
		path := filepath.Join(directory, file.name)
		if err := writePrivateKeyCommandFile(path, file.body); err != nil {
			cleanup()
			return err
		}
		created = append(created, path)
	}
	return nil
}

// writePrivateKeyCommandFile creates and synchronizes one non-overwriting 0600
// artifact.
func writePrivateKeyCommandFile(path string, body []byte) error {
	file, err := os.OpenFile(
		path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600,
	)
	if err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("backup keys: output file already exists")
		}
		return fmt.Errorf("backup keys: create output file: %w", err)
	}
	ok := false
	defer func() {
		_ = file.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(body); err != nil {
		return fmt.Errorf("backup keys: write output file: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("backup keys: sync output file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("backup keys: close output file: %w", err)
	}
	ok = true
	return nil
}

// readRecoveryKeyFile decodes exactly one private 256-bit recovery key.
func readRecoveryKeyFile(path string) ([]byte, error) {
	body, err := readPrivateKeyCommandFile(
		path, maxKeyCommandRecoveryKeyBytes,
	)
	if err != nil {
		return nil, err
	}
	defer zeroKeyCommandBytes(body)
	encoded := bytes.TrimSpace(body)
	key := make([]byte, base64.StdEncoding.DecodedLen(len(encoded)))
	decoded, err := base64.StdEncoding.Decode(key, encoded)
	key = key[:decoded]
	if err != nil || len(key) != 32 {
		zeroKeyCommandBytes(key)
		return nil, fmt.Errorf(
			"backup keys: recovery key encoding is invalid",
		)
	}
	return key, nil
}

// readPrivateKeyCommandFile rejects links, broad permissions, replacement
// races, empty inputs, and data beyond the command-specific bound.
func readPrivateKeyCommandFile(
	path string,
	maxBytes int64,
) ([]byte, error) {
	body, err := backupkeys.ReadProtectedDeploymentFile(path, maxBytes)
	if err != nil {
		return nil, fmt.Errorf("backup keys: %w", err)
	}
	return body, nil
}

func writeKeyMetadata(
	output io.Writer,
	metadata backupkeys.DeploymentKeyPackageMetadata,
) error {
	var body bytes.Buffer
	encoder := json.NewEncoder(&body)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(metadata); err != nil {
		return keyCommandExit(
			fmt.Sprintf("encode key metadata: %v", err),
		)
	}
	if _, err := io.Copy(output, &body); err != nil {
		return keyCommandExit(
			fmt.Sprintf("write key metadata: %v", err),
		)
	}
	return nil
}

func keyCommandExit(message string) error {
	return command.Exit{
		Code: command.ExitConfig, Message: message,
	}
}

func zeroKeyCommandBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
