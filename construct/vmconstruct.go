package construct

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"time"
	"unicode/utf16"

	. "github.com/cloudfoundry-incubator/stembuild/remotemanager"
)

//go:generate counterfeiter . VersionGetter
type VersionGetter interface {
	GetVersion() string
}

type VMConstruct struct {
	ctx                   context.Context
	remoteManager         RemoteManager
	Client                IaasClient
	guestManager          GuestManager
	vmInventoryPath       string
	vmUsername            string
	vmPassword            string
	winRMEnabler          WinRMEnabler
	vmConnectionValidator VMConnectionValidator
	messenger             ConstructMessenger
	poller                Poller
	versionGetter         VersionGetter
}

const provisionDir = "C:\\provision\\"
const stemcellAutomationName = "StemcellAutomation.zip"
const stemcellAutomationDest = provisionDir + stemcellAutomationName
const lgpoDest = provisionDir + "LGPO.zip"
const stemcellAutomationScript = provisionDir + "Setup.ps1"
const powershell = "C:\\Windows\\System32\\WindowsPowerShell\\V1.0\\powershell.exe"
const boshPsModules = "bosh-psmodules.zip"
const winRMPsScript = "BOSH.WinRM.psm1"

func NewVMConstruct(
	ctx context.Context,
	remoteManager RemoteManager,
	vmUsername,
	vmPassword,
	vmInventoryPath string,
	client IaasClient,
	guestManager GuestManager,
	winRMEnabler WinRMEnabler,
	vmConnectionValidator VMConnectionValidator,
	messenger ConstructMessenger,
	poller Poller,
	versionGetter VersionGetter,
) *VMConstruct {

	return &VMConstruct{
		ctx,
		remoteManager,
		client,
		guestManager,
		vmInventoryPath,
		vmUsername,
		vmPassword,
		winRMEnabler,
		vmConnectionValidator,
		messenger,
		poller,
		versionGetter,
	}
}

//go:generate counterfeiter . GuestManager
type GuestManager interface {
	ExitCodeForProgramInGuest(ctx context.Context, pid int64) (int32, error)
	StartProgramInGuest(ctx context.Context, command, args string) (int64, error)
	DownloadFileInGuest(ctx context.Context, path string) (io.Reader, int64, error)
}

//go:generate counterfeiter . IaasClient
type IaasClient interface {
	UploadArtifact(vmInventoryPath, artifact, destination, username, password string) error
	MakeDirectory(vmInventoryPath, path, username, password string) error
	Start(vmInventoryPath, username, password, command string, args ...string) (string, error)
	WaitForExit(vmInventoryPath, username, password, pid string) (int, error)
	IsPoweredOff(vmInventoryPath string) (bool, error)
}

//go:generate counterfeiter . WinRMEnabler
type WinRMEnabler interface {
	Enable() error
}

//go:generate counterfeiter . VMConnectionValidator
type VMConnectionValidator interface {
	Validate() error
}

//go:generate counterfeiter . ConstructMessenger
type ConstructMessenger interface {
	CreateProvisionDirStarted()
	CreateProvisionDirSucceeded()
	UploadArtifactsStarted()
	UploadArtifactsSucceeded()
	EnableWinRMStarted()
	EnableWinRMSucceeded()
	ValidateVMConnectionStarted()
	ValidateVMConnectionSucceeded()
	ExtractArtifactsStarted()
	ExtractArtifactsSucceeded()
	ExecuteScriptStarted()
	ExecuteScriptSucceeded()
	UploadFileStarted(artifact string)
	UploadFileSucceeded()
	RestartInProgress()
	ShutdownCompleted()
	WinRMDisconnectedForReboot()
	LogOutUsersStarted()
	LogOutUsersSucceeded()
}

//go:generate counterfeiter . Poller
type Poller interface {
	Poll(duration time.Duration, loopFunc func() (bool, error)) error
}

func (c *VMConstruct) PrepareVM() error {
	stembuildVersion := c.versionGetter.GetVersion()

	err := c.createProvisionDirectory()
	if err != nil {
		return err
	}
	c.messenger.UploadArtifactsStarted()
	err = c.uploadArtifacts()
	if err != nil {
		return err
	}
	c.messenger.UploadArtifactsSucceeded()

	c.messenger.EnableWinRMStarted()
	err = c.winRMEnabler.Enable()
	if err != nil {
		return err
	}
	c.messenger.EnableWinRMSucceeded()

	c.messenger.ValidateVMConnectionStarted()
	err = c.vmConnectionValidator.Validate()
	if err != nil {
		return err
	}
	c.messenger.ValidateVMConnectionSucceeded()

	c.messenger.ExtractArtifactsStarted()
	err = c.extractArchive()
	if err != nil {
		return err
	}
	c.messenger.ExtractArtifactsSucceeded()

	c.messenger.LogOutUsersStarted()
	err = c.logOutUsers()
	if err != nil {
		return err
	}
	c.messenger.LogOutUsersSucceeded()

	c.messenger.ExecuteScriptStarted()
	err = c.executeSetupScript(stembuildVersion)
	if err != nil {
		return err
	}
	c.messenger.ExecuteScriptSucceeded()
	c.messenger.WinRMDisconnectedForReboot()

	err = c.isPoweredOff(time.Minute)
	if err != nil {
		return err
	}

	return nil
}

func (c *VMConstruct) createProvisionDirectory() error {
	c.messenger.CreateProvisionDirStarted()
	err := c.Client.MakeDirectory(c.vmInventoryPath, provisionDir, c.vmUsername, c.vmPassword)
	if err != nil {
		return err
	}
	c.messenger.CreateProvisionDirSucceeded()
	return nil
}

func (c *VMConstruct) uploadArtifacts() error {
	c.messenger.UploadFileStarted("LGPO")
	err := c.Client.UploadArtifact(c.vmInventoryPath, "./LGPO.zip", lgpoDest, c.vmUsername, c.vmPassword)
	if err != nil {
		return err
	}
	c.messenger.UploadFileSucceeded()

	c.messenger.UploadFileStarted("stemcell preparation artifacts")
	err = c.Client.UploadArtifact(c.vmInventoryPath, fmt.Sprintf("./%s", stemcellAutomationName), stemcellAutomationDest, c.vmUsername, c.vmPassword)
	if err != nil {
		return err
	}
	c.messenger.UploadFileSucceeded()

	return nil
}

func (c *VMConstruct) extractArchive() error {
	err := c.remoteManager.ExtractArchive(stemcellAutomationDest, provisionDir)
	return err
}

func (c *VMConstruct) logOutUsers() error {
	failureString := "failed to log out remote user: %s"
	rawLogoffCommand := []byte("$(Get-WmiObject win32_operatingsystem).Win32Shutdown(0)")
	logoffCommand := encodePowershellCommand(rawLogoffCommand)

	pid, err := c.Client.Start(c.vmInventoryPath, c.vmUsername, c.vmPassword, powershell, "-EncodedCommand", logoffCommand)

	if err != nil {
		return fmt.Errorf(failureString, err)
	}

	exitCode, err := c.Client.WaitForExit(c.vmInventoryPath, c.vmUsername, c.vmPassword, pid)

	if err != nil {
		return fmt.Errorf(failureString, err)
	}
	if exitCode != 0 {
		return fmt.Errorf(failureString, fmt.Sprintf("logout process on VM exited with code %d", exitCode))
	}
	return nil
}

func (c *VMConstruct) executeSetupScript(stembuildVersion string) error {
	versionArg := " -Version " + stembuildVersion
	err := c.remoteManager.ExecuteCommand("powershell.exe " + stemcellAutomationScript + versionArg)
	return err
}

func (c *VMConstruct) isPoweredOff(duration time.Duration) error {
	err := c.poller.Poll(duration, func() (bool, error) {
		isPoweredOff, err := c.Client.IsPoweredOff(c.vmInventoryPath)

		if err != nil {
			return false, err
		}

		c.messenger.RestartInProgress()

		return isPoweredOff, nil
	})

	if err != nil {
		return err
	}

	c.messenger.ShutdownCompleted()
	return nil
}

func encodePowershellCommand(command []byte) string {
	runeCommand := []rune(string(command))
	utf16Command := utf16.Encode(runeCommand)
	byteCommand := &bytes.Buffer{}
	for _, utf16char := range utf16Command {
		b := make([]byte, 2)
		binary.LittleEndian.PutUint16(b, utf16char)
		byteCommand.Write(b) // This write never returns an error.
	}
	return base64.StdEncoding.EncodeToString(byteCommand.Bytes())
}
