package construct_test

import (
	"errors"

	"github.com/cloudfoundry-incubator/stembuild/assets"

	. "github.com/cloudfoundry-incubator/stembuild/construct"
	"github.com/cloudfoundry-incubator/stembuild/construct/constructfakes"
	"github.com/cloudfoundry-incubator/stembuild/remotemanager/remotemanagerfakes"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("construct_helpers", func() {
	var (
		fakeRemoteManager *remotemanagerfakes.FakeRemoteManager
		vmConstruct       *VMConstruct
		fakeVcenterClient *constructfakes.FakeIaasClient
		fakeZipUnarchiver *constructfakes.FakeZipUnarchiver
		fakeMessenger     *constructfakes.FakeConstructMessenger
	)

	BeforeEach(func() {
		fakeRemoteManager = &remotemanagerfakes.FakeRemoteManager{}
		fakeVcenterClient = &constructfakes.FakeIaasClient{}
		fakeZipUnarchiver = &constructfakes.FakeZipUnarchiver{}
		fakeMessenger = &constructfakes.FakeConstructMessenger{}
		vmConstruct = NewMockVMConstruct(
			fakeRemoteManager,
			fakeVcenterClient,
			"fakeVmPath",
			"fakeUser",
			"fakePass",
			fakeZipUnarchiver,
			fakeMessenger,
		)
	})

	Describe("PrepareVM", func() {
		Context("can create provision directory", func() {
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

		Context("enable WinRM", func() {
			var saByteData []byte

			BeforeEach(func() {
				var err error
				saByteData, err = assets.Asset("StemcellAutomation.zip")
				Expect(err).ToNot(HaveOccurred())
			})

			It("returns failure when it fails to enable winrm", func() {
				execError := errors.New("failed to execute setup script")
				fakeVcenterClient.StartReturns("", execError)

				err := vmConstruct.PrepareVM()
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(Equal("failed to enable WinRM: failed to execute setup script"))

				Expect(fakeVcenterClient.StartCallCount()).To(Equal(1))
			})

			It("returns failure when it fails to poll for enable WinRM process on guest vm", func() {
				fakeVcenterClient.StartReturns("1456", nil)

				execError := errors.New("failed to find PID")
				fakeVcenterClient.WaitForExitReturns(1, execError)

				err := vmConstruct.PrepareVM()
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(Equal("failed to enable WinRM: failed to find PID"))

				Expect(fakeVcenterClient.StartCallCount()).To(Equal(1))
				Expect(fakeVcenterClient.WaitForExitCallCount()).To(Equal(1))
				_, _, _, pid := fakeVcenterClient.WaitForExitArgsForCall(0)

				Expect(pid).To(Equal("1456"))
			})

			It("returns failure when WinRM process on guest VM exited with non zero exit code", func() {
				fakeVcenterClient.StartReturns("1456", nil)

				fakeVcenterClient.WaitForExitReturns(120, nil)

				err := vmConstruct.PrepareVM()
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(Equal("failed to enable WinRM: WinRM process on guest VM exited with code 120"))

				Expect(fakeVcenterClient.StartCallCount()).To(Equal(1))
				Expect(fakeVcenterClient.WaitForExitCallCount()).To(Equal(1))
			})

			It("returns a failure when it fails to find bosh-psmodules.zip in the achive artifact", func() {
				execError := errors.New("failed to find bosh-psmodules.zip")
				fakeZipUnarchiver.UnzipReturnsOnCall(0, nil, execError)

				err := vmConstruct.PrepareVM()
				Expect(err).To(HaveOccurred())
				Expect(err).To(MatchError("failed to enable WinRM: failed to find bosh-psmodules.zip"))
				Expect(fakeZipUnarchiver.UnzipCallCount()).To(Equal(1))

				archive, fileName := fakeZipUnarchiver.UnzipArgsForCall(0)

				Expect(fileName).To(Equal("bosh-psmodules.zip"))
				Expect(archive).To(Equal(saByteData))

				Expect(fakeVcenterClient.StartCallCount()).To(Equal(0))
				Expect(fakeVcenterClient.WaitForExitCallCount()).To(Equal(0))

			})

			It("returns a failure when fails to find BOSH.WinRM.psm1 in bosh-psmodules.zip", func() {
				execError := errors.New("failed to find BOSH.WinRM.psm1")
				fakeZipUnarchiver.UnzipReturnsOnCall(0, []byte("bosh-psmodules.zip extracted byte array"), nil)
				fakeZipUnarchiver.UnzipReturnsOnCall(1, nil, execError)

				err := vmConstruct.PrepareVM()
				Expect(err).To(HaveOccurred())
				Expect(err).To(MatchError("failed to enable WinRM: failed to find BOSH.WinRM.psm1"))
				Expect(fakeZipUnarchiver.UnzipCallCount()).To(Equal(2))

				archive, fileName := fakeZipUnarchiver.UnzipArgsForCall(0)
				Expect(fileName).To(Equal("bosh-psmodules.zip"))
				Expect(archive).To(Equal(saByteData))

				archive, fileName = fakeZipUnarchiver.UnzipArgsForCall(1)
				Expect(fileName).To(Equal("BOSH.WinRM.psm1"))
				Expect(archive).To(Equal([]byte("bosh-psmodules.zip extracted byte array")))

				Expect(fakeVcenterClient.StartCallCount()).To(Equal(0))
				Expect(fakeVcenterClient.WaitForExitCallCount()).To(Equal(0))
			})

			It("returns success when it enables WinRM on the guest VM", func() {
				fakeVcenterClient.StartReturns("65535", nil)
				fakeZipUnarchiver.UnzipReturnsOnCall(0, []byte("bosh-psmodules.zip extracted byte array"), nil)
				fakeZipUnarchiver.UnzipReturnsOnCall(1, []byte("BOSH.WinRM.psm1 extracted byte array"), nil)

				err := vmConstruct.PrepareVM()
				Expect(err).ToNot(HaveOccurred())

				Expect(fakeZipUnarchiver.UnzipCallCount()).To(Equal(2))
				Expect(fakeVcenterClient.StartCallCount()).To(Equal(2))
				Expect(fakeVcenterClient.WaitForExitCallCount()).To(Equal(2))

				archive, fileName := fakeZipUnarchiver.UnzipArgsForCall(0)
				Expect(fileName).To(Equal("bosh-psmodules.zip"))
				Expect(archive).To(Equal(saByteData))

				archive, fileName = fakeZipUnarchiver.UnzipArgsForCall(1)
				Expect(fileName).To(Equal("BOSH.WinRM.psm1"))
				Expect(archive).To(Equal([]byte("bosh-psmodules.zip extracted byte array")))

				vmInventoryPath, username, password, command, args := fakeVcenterClient.StartArgsForCall(0)
				Expect(vmInventoryPath).To(Equal("fakeVmPath"))
				Expect(username).To(Equal("fakeUser"))
				Expect(password).To(Equal("fakePass"))
				// Though the directory uses v1.0, this is also valid for Powershell 5 that we require
				Expect(command).To(Equal("C:\\Windows\\System32\\WindowsPowerShell\\V1.0\\powershell.exe"))
				// The encoded string was created by running the following in terminal `printf "BOSH.WinRM.psm1 extracted byte array\nEnable-WinRM" | iconv -t UTF-16LE | openssl base64 | tr -d '\n'`
				Expect(args).To(Equal([]string{"-EncodedCommand", "QgBPAFMASAAuAFcAaQBuAFIATQAuAHAAcwBtADEAIABlAHgAdAByAGEAYwB0AGUAZAAgAGIAeQB0AGUAIABhAHIAcgBhAHkACgBFAG4AYQBiAGwAZQAtAFcAaQBuAFIATQAKAA=="}))

				vmInventoryPath, username, password, pid := fakeVcenterClient.WaitForExitArgsForCall(0)
				Expect(vmInventoryPath).To(Equal("fakeVmPath"))
				Expect(username).To(Equal("fakeUser"))
				Expect(password).To(Equal("fakePass"))
				Expect(pid).To(Equal("65535"))
			})

			It("logs that winrm was succesfully enabled", func() {
				err := vmConstruct.PrepareVM()

				Expect(err).NotTo(HaveOccurred())
				Expect(fakeMessenger.EnableWinRMStartedCallCount()).To(Equal(1))
				Expect(fakeMessenger.EnableWinRMSucceededCallCount()).To(Equal(1))
			})
		})

		Context("can connect to VM", func() {
			It("can reach VM and can login to VM", func() {
				err := vmConstruct.PrepareVM()

				Expect(err).To(BeNil())
				Expect(fakeRemoteManager.CanReachVMCallCount()).To(Equal(1))
				Expect(fakeRemoteManager.CanLoginVMCallCount()).To(Equal(1))
			})
			It("returns an error if it cannot reach the VM", func() {
				fakeRemoteManager.CanReachVMReturns(errors.New("can't reach VM"))

				err := vmConstruct.PrepareVM()
				Expect(err).NotTo(BeNil())
				Expect(err).To(MatchError("can't reach VM"))
				Expect(fakeRemoteManager.CanReachVMCallCount()).To(Equal(1))
				Expect(fakeRemoteManager.CanLoginVMCallCount()).To(Equal(0))
			})

			It("should return an error when login fails", func() {
				invalidPwdError := errors.New("login error")
				fakeRemoteManager.CanLoginVMReturns(invalidPwdError)

				err := vmConstruct.PrepareVM()
				Expect(err).To(HaveOccurred())
				Expect(err).To(MatchError(invalidPwdError))

				Expect(fakeRemoteManager.CanReachVMCallCount()).To(Equal(1))
				Expect(fakeRemoteManager.CanLoginVMCallCount()).To(Equal(1))
			})

			It("logs that it successfully validated the vm connection", func() {
				err := vmConstruct.PrepareVM()

				Expect(err).NotTo(HaveOccurred())
				Expect(fakeMessenger.ValidateVMConnectionStartedCallCount()).To(Equal(1))
				Expect(fakeMessenger.ValidateVMConnectionSucceededCallCount()).To(Equal(1))
			})

		})

		Context("can upload artifacts", func() {
			BeforeEach(func() {
				fakeZipUnarchiver.UnzipReturns([]byte("extracted archive"), nil)
				fakeVcenterClient.StartReturns("1167", nil)
			})

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


		Context("can log out remote users", func() {
			It("returns success when it logs out users", func() {

				fakeVcenterClient.StartReturnsOnCall(1, "5555", nil)

				err := vmConstruct.PrepareVM()
				Expect(err).ToNot(HaveOccurred())

				vmPath, user, pass, command, args := fakeVcenterClient.StartArgsForCall(1)


				Expect(command).To(Equal("C:\\Windows\\System32\\WindowsPowerShell\\V1.0\\powershell.exe"))
				Expect(vmPath).To(Equal("fakeVmPath"))
				Expect(args).To(Equal([]string{"-EncodedCommand"}))
				Expect(user).To(Equal("fakeUser"))
				Expect(pass).To(Equal("fakePass"))
				Expect(fakeVcenterClient.StartCallCount()).To(Equal(2))

				Expect(fakeMessenger.LogOutUsersStartedCallCount()).To(Equal(1))
				Expect(fakeMessenger.LogOutUsersSucceededCallCount()).To(Equal(1))


				vmInventoryPath, username, password, pid := fakeVcenterClient.WaitForExitArgsForCall(1)
				Expect(vmInventoryPath).To(Equal("fakeVmPath"))
				Expect(username).To(Equal("fakeUser"))
				Expect(password).To(Equal("fakePass"))
				Expect(pid).To(Equal("5555"))

			})
			It("returns failure when it does not log out users", func() {

			})
		})

		Context("can extract archives", func() {
			BeforeEach(func() {
				fakeZipUnarchiver.UnzipReturns([]byte("extracted archive"), nil)
				fakeVcenterClient.StartReturns("1167", nil)
			})

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

		Context("can execute scripts", func() {
			BeforeEach(func() {
				fakeZipUnarchiver.UnzipReturns([]byte("extracted archive"), nil)
				fakeVcenterClient.StartReturns("1167", nil)
			})
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

				err := vmConstruct.PrepareVM()
				Expect(err).ToNot(HaveOccurred())

				Expect(fakeRemoteManager.ExecuteCommandCallCount()).To(Equal(1))
				command := fakeRemoteManager.ExecuteCommandArgsForCall(0)
				Expect(command).To(Equal("powershell.exe C:\\provision\\Setup.ps1"))

				Expect(fakeMessenger.ExecuteScriptStartedCallCount()).To(Equal(1))
				Expect(fakeMessenger.ExecuteScriptSucceededCallCount()).To(Equal(1))
			})

		})

	})
})
