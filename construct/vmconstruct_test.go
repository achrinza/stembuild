package construct_test

import (
	"context"
	"errors"
	"time"

	"github.com/onsi/gomega/gbytes"

	. "github.com/cloudfoundry-incubator/stembuild/construct"
	"github.com/cloudfoundry-incubator/stembuild/construct/constructfakes"
	"github.com/cloudfoundry-incubator/stembuild/remotemanager/remotemanagerfakes"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("construct_helpers", func() {
	var (
		fakeRemoteManager         *remotemanagerfakes.FakeRemoteManager
		vmConstruct               *VMConstruct
		fakeVcenterClient         *constructfakes.FakeIaasClient
		fakeGuestManager          *constructfakes.FakeGuestManager
		fakeWinRMEnabler          *constructfakes.FakeWinRMEnabler
		fakeOSValidator           *constructfakes.FakeOSValidator
		fakeMessenger             *constructfakes.FakeConstructMessenger
		fakePoller                *constructfakes.FakePoller
		fakeVersionGetter         *constructfakes.FakeVersionGetter
		fakeVMConnectionValidator *constructfakes.FakeVMConnectionValidator
	)

	BeforeEach(func() {
		fakeRemoteManager = &remotemanagerfakes.FakeRemoteManager{}
		fakeVcenterClient = &constructfakes.FakeIaasClient{}
		fakeGuestManager = &constructfakes.FakeGuestManager{}
		fakeWinRMEnabler = &constructfakes.FakeWinRMEnabler{}
		fakeOSValidator = &constructfakes.FakeOSValidator{}
		fakeMessenger = &constructfakes.FakeConstructMessenger{}
		fakePoller = &constructfakes.FakePoller{}
		fakeVersionGetter = &constructfakes.FakeVersionGetter{}
		fakeVMConnectionValidator = &constructfakes.FakeVMConnectionValidator{}

		vmConstruct = NewVMConstruct(
			context.TODO(),
			fakeRemoteManager,
			"fakeUser",
			"fakePass",
			"fakeVmPath",
			fakeVcenterClient,
			fakeGuestManager,
			fakeWinRMEnabler,
			fakeOSValidator,
			fakeVMConnectionValidator,
			fakeMessenger,
			fakePoller,
			fakeVersionGetter,
		)

		fakeGuestManager.StartProgramInGuestReturnsOnCall(0, 0, nil)
		fakeGuestManager.ExitCodeForProgramInGuestReturnsOnCall(0, 0, nil)
		versionBuffer := gbytes.NewBuffer()
		_, err := versionBuffer.Write([]byte("dev"))
		Expect(err).NotTo(HaveOccurred())

		fakeGuestManager.DownloadFileInGuestReturns(versionBuffer, 3, nil)
		fakeGuestManager.StartProgramInGuestReturns(0, nil)

	})

	Describe("PrepareVM", func() {

		Describe("Validates the OS version of the target machine", func() {
			It("returns failure if the OS Validator returns an error", func() {
				validationError := errors.New("the OS is wrong")
				fakeOSValidator.ValidateReturns(validationError)

				err := vmConstruct.PrepareVM()

				Expect(err).To(MatchError(validationError))
				Expect(fakeVcenterClient.MakeDirectoryCallCount()).To(Equal(0))

				Expect(fakeMessenger.UploadArtifactsStartedCallCount()).To(Equal(0))
			})

			It("prepares the VM if the OS version is correct", func() {
				err := vmConstruct.PrepareVM()

				Expect(err).NotTo(HaveOccurred())
				Expect(fakeMessenger.UploadArtifactsStartedCallCount()).To(Equal(1))
			})
		})

		Describe("can create provision directory", func() {
			It("creates it successfully", func() {
				err := vmConstruct.PrepareVM()

				Expect(err).ToNot(HaveOccurred())
				Expect(fakeVcenterClient.MakeDirectoryCallCount()).To(Equal(1))
				Expect(fakeMessenger.CreateProvisionDirStartedCallCount()).To(Equal(1))
				Expect(fakeMessenger.CreateProvisionDirSucceededCallCount()).To(Equal(1))
			})

			It("fails when the provision dir cannot be created", func() {
				mkDirError := errors.New("failed to create dir")
				fakeVcenterClient.MakeDirectoryReturns(mkDirError)

				err := vmConstruct.PrepareVM()

				Expect(fakeVcenterClient.MakeDirectoryCallCount()).To(Equal(1))
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(Equal("failed to create dir"))
				Expect(fakeMessenger.CreateProvisionDirStartedCallCount()).To(Equal(1))
				Expect(fakeMessenger.CreateProvisionDirSucceededCallCount()).To(Equal(0))
			})
		})

		Describe("enable WinRM", func() {
			It("returns failure when it fails to enable winrm", func() {
				execError := errors.New("failed to enable winRM")
				fakeWinRMEnabler.EnableReturns(execError)

				err := vmConstruct.PrepareVM()
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(Equal("failed to enable winRM"))

				Expect(fakeWinRMEnabler.EnableCallCount()).To(Equal(1))
			})

			It("logs that winrm was successfully enabled", func() {
				err := vmConstruct.PrepareVM()

				Expect(err).NotTo(HaveOccurred())
				Expect(fakeMessenger.EnableWinRMStartedCallCount()).To(Equal(1))
				Expect(fakeMessenger.EnableWinRMSucceededCallCount()).To(Equal(1))
			})
		})

		Describe("connect to VM", func() {

			It("checks for WinRM connectivity after WinRM enabled", func() {
				var calls []string

				fakeWinRMEnabler.EnableCalls(func() error {
					calls = append(calls, "enableWinRMCall")
					return nil
				})

				fakeVMConnectionValidator.ValidateCalls(func() error {
					calls = append(calls, "validateVMConnCall")
					return nil
				})

				err := vmConstruct.PrepareVM()
				Expect(err).NotTo(HaveOccurred())

				Expect(calls[0]).To(Equal("enableWinRMCall"))
				Expect(calls[1]).To(Equal("validateVMConnCall"))
			})

			It("logs that it successfully validated the vm connection", func() {
				err := vmConstruct.PrepareVM()

				Expect(err).NotTo(HaveOccurred())
				Expect(fakeMessenger.ValidateVMConnectionStartedCallCount()).To(Equal(1))
				Expect(fakeMessenger.ValidateVMConnectionSucceededCallCount()).To(Equal(1))
			})

		})

		Describe("can upload artifacts", func() {
			Context("Upload all artifacts correctly", func() {
				It("passes successfully", func() {

					err := vmConstruct.PrepareVM()
					Expect(err).ToNot(HaveOccurred())
					vmPath, artifact, dest, user, pass := fakeVcenterClient.UploadArtifactArgsForCall(0)
					Expect(artifact).To(Equal("./LGPO.zip"))
					Expect(vmPath).To(Equal("fakeVmPath"))
					Expect(dest).To(Equal("C:\\provision\\LGPO.zip"))
					Expect(user).To(Equal("fakeUser"))
					Expect(pass).To(Equal("fakePass"))
					Expect(fakeVcenterClient.UploadArtifactCallCount()).To(Equal(2))
					Expect(fakeMessenger.UploadArtifactsStartedCallCount()).To(Equal(1))
					Expect(fakeMessenger.UploadArtifactsSucceededCallCount()).To(Equal(1))

					Expect(fakeMessenger.UploadFileStartedCallCount()).To(Equal(2))
					artifact = fakeMessenger.UploadFileStartedArgsForCall(0)
					Expect(artifact).To(Equal("LGPO"))
					artifact = fakeMessenger.UploadFileStartedArgsForCall(1)
					Expect(artifact).To(Equal("stemcell preparation artifacts"))

					Expect(fakeMessenger.UploadFileSucceededCallCount()).To(Equal(2))
				})

			})

			Context("Fails to upload one or more artifacts", func() {
				It("fails when it cannot upload LGPO", func() {

					uploadError := errors.New("failed to upload LGPO")
					fakeVcenterClient.UploadArtifactReturns(uploadError)

					err := vmConstruct.PrepareVM()
					Expect(err).To(HaveOccurred())
					Expect(err.Error()).To(Equal("failed to upload LGPO"))

					vmPath, artifact, _, _, _ := fakeVcenterClient.UploadArtifactArgsForCall(0)
					Expect(artifact).To(Equal("./LGPO.zip"))
					Expect(vmPath).To(Equal("fakeVmPath"))
					Expect(fakeVcenterClient.UploadArtifactCallCount()).To(Equal(1))
					Expect(fakeMessenger.UploadArtifactsStartedCallCount()).To(Equal(1))
					Expect(fakeMessenger.UploadArtifactsSucceededCallCount()).To(Equal(0))
				})

				It("fails when it cannot upload Stemcell Automation scripts", func() {

					uploadError := errors.New("failed to upload stemcell automation")
					fakeVcenterClient.UploadArtifactReturnsOnCall(0, nil)
					fakeVcenterClient.UploadArtifactReturnsOnCall(1, uploadError)

					err := vmConstruct.PrepareVM()
					Expect(err).To(HaveOccurred())
					Expect(err.Error()).To(Equal("failed to upload stemcell automation"))

					vmPath, artifact, _, _, _ := fakeVcenterClient.UploadArtifactArgsForCall(0)
					Expect(artifact).To(Equal("./LGPO.zip"))
					Expect(vmPath).To(Equal("fakeVmPath"))
					vmPath, artifact, _, _, _ = fakeVcenterClient.UploadArtifactArgsForCall(1)
					Expect(artifact).To(Equal("./StemcellAutomation.zip"))
					Expect(vmPath).To(Equal("fakeVmPath"))
					Expect(fakeVcenterClient.UploadArtifactCallCount()).To(Equal(2))
					Expect(fakeMessenger.UploadArtifactsStartedCallCount()).To(Equal(1))
					Expect(fakeMessenger.UploadArtifactsSucceededCallCount()).To(Equal(0))
				})
			})
		})

		Describe("can extract archives", func() {
			It("returns failure when it fails to extract archive", func() {
				extractError := errors.New("failed to extract archive")
				fakeRemoteManager.ExtractArchiveReturns(extractError)

				err := vmConstruct.PrepareVM()
				Expect(err).To(HaveOccurred())
				Expect(fakeRemoteManager.ExtractArchiveCallCount()).To(Equal(1))
				Expect(err.Error()).To(Equal("failed to extract archive"))
				Expect(fakeMessenger.ExtractArtifactsStartedCallCount()).To(Equal(1))
				Expect(fakeMessenger.ExtractArtifactsSucceededCallCount()).To(Equal(0))
			})

			It("returns success when it properly extracts archive", func() {
				fakeRemoteManager.ExtractArchiveReturns(nil)

				err := vmConstruct.PrepareVM()
				Expect(err).ToNot(HaveOccurred())
				Expect(fakeRemoteManager.ExtractArchiveCallCount()).To(Equal(1))
				source, destination := fakeRemoteManager.ExtractArchiveArgsForCall(0)
				Expect(source).To(Equal("C:\\provision\\StemcellAutomation.zip"))
				Expect(destination).To(Equal("C:\\provision\\"))

				Expect(fakeMessenger.ExtractArtifactsStartedCallCount()).To(Equal(1))
				Expect(fakeMessenger.ExtractArtifactsSucceededCallCount()).To(Equal(1))
			})

		})

		Describe("can execute scripts", func() {
			It("returns failure when it fails to execute setup script", func() {
				execError := errors.New("failed to execute setup script")
				fakeRemoteManager.ExecuteCommandReturns(execError)

				err := vmConstruct.PrepareVM()
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(Equal("failed to execute setup script"))

				Expect(fakeRemoteManager.ExecuteCommandCallCount()).To(Equal(1))
				Expect(fakeMessenger.ExecuteScriptStartedCallCount()).To(Equal(1))
				Expect(fakeMessenger.ExecuteScriptSucceededCallCount()).To(Equal(0))
			})

			It("returns success when it properly executes the setup script", func() {
				fakeVersionGetter.GetVersionReturns("2019.123.456")

				err := vmConstruct.PrepareVM()
				Expect(err).ToNot(HaveOccurred())

				Expect(fakeRemoteManager.ExecuteCommandCallCount()).To(Equal(1))
				command := fakeRemoteManager.ExecuteCommandArgsForCall(0)
				Expect(command).To(Equal("powershell.exe C:\\provision\\Setup.ps1 -Version 2019.123.456"))

				Expect(fakeMessenger.ExecuteScriptStartedCallCount()).To(Equal(1))
				Expect(fakeMessenger.ExecuteScriptSucceededCallCount()).To(Equal(1))
			})

		})

		FDescribe("can wait for the vm to be shut down by sysprep", func() {
			It("polls every minute, logging that a restart is in progress if the VM is not powered off", func() {
				fakePoller.PollReturns(nil)

				fakeVcenterClient.HasBeenShutdownByVcenterTaskReturns(false, nil)

				fakeVcenterClient.IsPoweredOffReturnsOnCall(0, false, nil)
				fakeVcenterClient.IsPoweredOffReturnsOnCall(1, true, nil)
				fakeVcenterClient.IsPoweredOffReturnsOnCall(2, false, errors.New("checking for powered off is hard"))

				err := vmConstruct.PrepareVM()
				Expect(err).ToNot(HaveOccurred())

				Expect(fakePoller.PollCallCount()).To(Equal(1))
				pollDuration, pollFunc := fakePoller.PollArgsForCall(0)

				Expect(pollDuration).To(Equal(1 * time.Minute))

				Expect(fakeVcenterClient.IsPoweredOffCallCount()).To(Equal(0))
				Expect(fakeMessenger.RestartInProgressCallCount()).To(Equal(0))

				isPoweredOff, err := pollFunc()
				Expect(isPoweredOff).To(BeFalse())
				Expect(err).NotTo(HaveOccurred())
				Expect(fakeMessenger.RestartInProgressCallCount()).To(Equal(1))

				isPoweredOff, err = pollFunc()
				Expect(isPoweredOff).To(BeTrue())
				Expect(err).NotTo(HaveOccurred())
				Expect(fakeMessenger.RestartInProgressCallCount()).To(Equal(2))

				isPoweredOff, err = pollFunc()
				Expect(err).To(MatchError("checking for powered off is hard"))
				Expect(fakeMessenger.RestartInProgressCallCount()).To(Equal(2))

				Expect(fakeVcenterClient.IsPoweredOffCallCount()).To(Equal(3))
			})

			It("polls every minute and returns after logging that shutdown is complete, if it was not shutdown via vcenter", func() {
				fakePoller.PollReturns(nil)

				fakeVcenterClient.HasBeenShutdownByVcenterTaskReturns(false, nil)

				fakeVcenterClient.IsPoweredOffReturnsOnCall(0, false, nil)
				fakeVcenterClient.IsPoweredOffReturnsOnCall(1, true, nil)
				fakeVcenterClient.IsPoweredOffReturnsOnCall(2, false, errors.New("checking for powered off is hard"))

				err := vmConstruct.PrepareVM()
				Expect(err).ToNot(HaveOccurred())
				Expect(fakeMessenger.ShutdownCompletedCallCount()).To(Equal(1))

				Expect(fakePoller.PollCallCount()).To(Equal(1))
				pollDuration, pollFunc := fakePoller.PollArgsForCall(0)

				Expect(pollDuration).To(Equal(1 * time.Minute))

				Expect(fakeVcenterClient.IsPoweredOffCallCount()).To(Equal(0))
				Expect(fakeMessenger.RestartInProgressCallCount()).To(Equal(0))

				isPoweredOff, err := pollFunc()
				Expect(isPoweredOff).To(BeFalse())
				Expect(err).NotTo(HaveOccurred())
				Expect(fakeMessenger.RestartInProgressCallCount()).To(Equal(1))

				isPoweredOff, err = pollFunc()
				Expect(isPoweredOff).To(BeTrue())
				Expect(err).NotTo(HaveOccurred())
				Expect(fakeMessenger.RestartInProgressCallCount()).To(Equal(2))

				isPoweredOff, err = pollFunc()
				Expect(err).To(MatchError("checking for powered off is hard"))
				Expect(fakeMessenger.RestartInProgressCallCount()).To(Equal(2))

				Expect(fakeVcenterClient.IsPoweredOffCallCount()).To(Equal(3))
			})

			It("warns the user if the vm was shut down via the vcenter", func() {
				fakePoller.PollReturns(nil)
				fakeVcenterClient.IsPoweredOffReturns(true, nil)
				fakeVcenterClient.HasBeenShutdownByVcenterTaskReturns(true, nil)

				timeOfCall := time.Now()
				err := vmConstruct.PrepareVM()

				Expect(fakeVcenterClient.HasBeenShutdownByVcenterTaskCallCount()).To(Equal(1))

				path, threshholdTime := fakeVcenterClient.HasBeenShutdownByVcenterTaskArgsForCall(0)
				Expect(path).To(Equal("fakeVmPath"))
				Expect(threshholdTime).To(BeTemporally("~", timeOfCall, 100 * time.Millisecond))

				Expect(err).To(HaveOccurred())
				Expect(err).To(MatchError(ContainSubstring("VM was shutdown before sysprep completed")))
			})

			It("returns failure when it cannot determine vm power state", func() {
				fakePoller.PollReturns(errors.New("polling is hard"))

				Expect(vmConstruct.PrepareVM()).To(MatchError("polling is hard"))
				Expect(fakeMessenger.ShutdownCompletedCallCount()).To(Equal(0))
			})
		})

		FDescribe("can check if a vm shutdown was shutdown by a vcenter task", func() {

			It("logs that the vm shutdown safely if it was not shutdown via vcenter task", func() {

			})
		})
	})
})
