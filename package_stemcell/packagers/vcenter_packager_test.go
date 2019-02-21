package packagers_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha1"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"

	. "github.com/cloudfoundry-incubator/stembuild/package_stemcell/packagers"

	"github.com/cloudfoundry-incubator/stembuild/filesystem"
	"github.com/cloudfoundry-incubator/stembuild/package_stemcell/packagers/packagersfakes"

	"github.com/cloudfoundry-incubator/stembuild/package_stemcell/config"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("VcenterPackager", func() {

	var outputDir string
	var sourceConfig config.SourceConfig
	var outputConfig config.OutputConfig
	var fakeVcenterClient *packagersfakes.FakeIaasClient

	BeforeEach(func() {
		outputDir, _ = ioutil.TempDir(os.TempDir(), "vcenter-test-output-dir")
		sourceConfig = config.SourceConfig{Password: "password", URL: "url", Username: "username", VmInventoryPath: "path/valid-vm-name"}
		outputConfig = config.OutputConfig{Os: "2012R2", StemcellVersion: "1200.2", OutputDir: outputDir}
		fakeVcenterClient = &packagersfakes.FakeIaasClient{}
	})

	AfterEach(func() {
		_ = os.RemoveAll(outputDir)
	})

	Context("ValidateSourceParameters", func() {
		It("returns an error if the vCenter url is invalid", func() {

			fakeVcenterClient.ValidateUrlReturns(errors.New("invalid url"))

			packager := VCenterPackager{SourceConfig: sourceConfig, OutputConfig: outputConfig, Client: fakeVcenterClient}
			err := packager.ValidateSourceParameters()

			Expect(err).To(HaveOccurred())
			Expect(fakeVcenterClient.ValidateUrlCallCount()).To(Equal(1))
			Expect(err.Error()).To(Equal("please provide a valid vCenter URL"))

		})
		It("returns an error if the vCenter credentials are not valid", func() {

			fakeVcenterClient.ValidateCredentialsReturns(errors.New("invalid credentials"))

			packager := VCenterPackager{SourceConfig: sourceConfig, OutputConfig: outputConfig, Client: fakeVcenterClient}

			err := packager.ValidateSourceParameters()

			Expect(err).To(HaveOccurred())
			Expect(fakeVcenterClient.ValidateCredentialsCallCount()).To(Equal(1))
			Expect(err.Error()).To(ContainSubstring("please provide valid credentials for"))
		})

		It("returns an error if VM given does not exist ", func() {
			fakeVcenterClient.FindVMReturns(errors.New("invalid VM path"))

			packager := VCenterPackager{SourceConfig: sourceConfig, OutputConfig: outputConfig, Client: fakeVcenterClient}

			err := packager.ValidateSourceParameters()

			Expect(err).To(HaveOccurred())
			Expect(fakeVcenterClient.FindVMCallCount()).To(Equal(1))
			Expect(err.Error()).To(Equal("VM path is invalid\nPlease make sure to format your inventory path correctly using the 'vm' keyword. Example: /my-datacenter/vm/my-folder/my-vm-name"))
		})
		It("returns no error if all source parameters are valid", func() {

			packager := VCenterPackager{SourceConfig: sourceConfig, OutputConfig: outputConfig, Client: fakeVcenterClient}

			err := packager.ValidateSourceParameters()

			Expect(err).NotTo(HaveOccurred())
		})
	})
	Context("ValidateFreeSpace", func() {
		It("Print a warning to make sure that enough space is available", func() {
			packager := VCenterPackager{SourceConfig: sourceConfig, OutputConfig: outputConfig, Client: fakeVcenterClient}
			err := packager.ValidateFreeSpaceForPackage(&filesystem.OSFileSystem{})

			Expect(err).To(Not(HaveOccurred()))
		})
	})

	Describe("Package", func() {
		var packager *VCenterPackager

		AfterEach(func() {
			_ = os.RemoveAll("./valid-vm-name")
			_ = os.RemoveAll("image")
		})

		BeforeEach(func() {
			packager = &VCenterPackager{SourceConfig: sourceConfig, OutputConfig: outputConfig, Client: fakeVcenterClient}

			fakeVcenterClient.ExportVMStub = func(vmInventoryPath string, destination string) error {
				vmName := path.Base(vmInventoryPath)
				os.Mkdir(filepath.Join(destination, vmName), 0777)

				testOvfName := "valid-vm-name.content"
				err := ioutil.WriteFile(filepath.Join(filepath.Join(destination, vmName), testOvfName), []byte(""), 0777)
				Expect(err).NotTo(HaveOccurred())
				return nil
			}
		})

		It("creates a valid stemcell in the output directory", func() {
			err := packager.Package()

			Expect(err).To(Not(HaveOccurred()))
			stemcellFilename := StemcellFilename(packager.OutputConfig.StemcellVersion, packager.OutputConfig.Os)
			stemcellFile := filepath.Join(packager.OutputConfig.OutputDir, stemcellFilename)
			_, err = os.Stat(stemcellFile)

			Expect(err).NotTo(HaveOccurred())
			var actualStemcellManifestContent string
			expectedManifestContent := `---
name: bosh-vsphere-esxi-windows2012R2-go_agent
version: '1200.2'
sha1: %x
operating_system: windows2012R2
cloud_properties:
  infrastructure: vsphere
  hypervisor: esxi
stemcell_formats:
- vsphere-ovf
- vsphere-ova
`
			var fileReader, _ = os.OpenFile(stemcellFile, os.O_RDONLY, 0777)
			gzr, err := gzip.NewReader(fileReader)
			Expect(err).ToNot(HaveOccurred())
			defer gzr.Close()
			tarfileReader := tar.NewReader(gzr)
			count := 0

			for {
				header, err := tarfileReader.Next()
				if err == io.EOF {
					break
				}

				Expect(err).NotTo(HaveOccurred())

				switch filepath.Base(header.Name) {
				case "stemcell.MF":
					buf := new(bytes.Buffer)
					_, err = buf.ReadFrom(tarfileReader)
					Expect(err).NotTo(HaveOccurred())
					count++

					actualStemcellManifestContent = buf.String()

				case "image":
					count++
					actualSha1 := sha1.New()
					io.Copy(actualSha1, tarfileReader)

					expectedManifestContent = fmt.Sprintf(expectedManifestContent, actualSha1.Sum(nil))

				default:

					Fail(fmt.Sprintf("Found unknown file: %s", filepath.Base(header.Name)))
				}
			}
			Expect(count).To(Equal(2))
			Expect(actualStemcellManifestContent).To(Equal(expectedManifestContent))
		})

		It("removes all ethernet and floppy devices", func() {
			fullDeviceList := []string{"video-674", "cdrom-12", "ps2-450", "ethernet-1", "floppy-8000", "floppy-9000", "video-500"}
			expectedDeviceList := []string{"ethernet-1", "floppy-8000", "floppy-9000"}
			fakeVcenterClient.ListDevicesReturns(fullDeviceList, nil)

			err := packager.Package()

			Expect(err).NotTo(HaveOccurred())

			for i, device := range expectedDeviceList {
				vmPath, deviceName := fakeVcenterClient.RemoveDeviceArgsForCall(i)
				Expect(vmPath).To(Equal(sourceConfig.VmInventoryPath))
				Expect(deviceName).To(Equal(device))
			}
		})

		It("ejects all CD ROM devices", func() {
			fullDeviceList := []string{"video-674", "cdrom-12", "ps2-450", "ethernet-1", "cdrom-123"}
			expectedDeviceList := []string{"cdrom-12", "cdrom-123"}
			fakeVcenterClient.ListDevicesReturns(fullDeviceList, nil)

			err := packager.Package()

			Expect(err).NotTo(HaveOccurred())

			for i, device := range expectedDeviceList {
				vmPath, deviceName := fakeVcenterClient.EjectCDRomArgsForCall(i)
				Expect(vmPath).To(Equal(sourceConfig.VmInventoryPath))
				Expect(deviceName).To(Equal(device))
			}
		})

		It("Throws an error if the VCenter client fails to list devices", func() {
			listDevicesError := errors.New(fmt.Sprintf("VCenter failed to list devices for: %s", sourceConfig.VmInventoryPath))
			fakeVcenterClient.ListDevicesReturns([]string{}, listDevicesError)

			err := packager.Package()
			Expect(err).To(HaveOccurred())
			Expect(err).To(MatchError(listDevicesError))
		})

		It("Throws an error if the VCenter client fails to remove a device", func() {
			removeDeviceError := errors.New(fmt.Sprintf("VCenter failed to remove a device from: %s", sourceConfig.VmInventoryPath))

			fakeVcenterClient.ListDevicesReturns([]string{"floppy-8000"}, nil)
			fakeVcenterClient.RemoveDeviceReturns(removeDeviceError)

			err := packager.Package()
			Expect(err).To(HaveOccurred())
			Expect(err).To(MatchError(removeDeviceError))
		})

		It("Returns a error message if exporting the VM fails", func() {
			packager := VCenterPackager{SourceConfig: sourceConfig, OutputConfig: outputConfig, Client: fakeVcenterClient}
			fakeVcenterClient.ExportVMReturns(errors.New(fmt.Sprintf(sourceConfig.VmInventoryPath + " could not be exported")))
			err := packager.Package()

			Expect(err).To(HaveOccurred())
			Expect(fakeVcenterClient.ExportVMCallCount()).To(Equal(1))
			vmPath, _ := fakeVcenterClient.ExportVMArgsForCall(0)
			Expect(vmPath).To(Equal(sourceConfig.VmInventoryPath))
			Expect(err.Error()).To(Equal("failed to export the prepared VM"))
		})
	})
})
