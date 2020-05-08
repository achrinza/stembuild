package construct_test

import (
	"bufio"
	"bytes"
	"fmt"
	"github.com/cloudfoundry-incubator/stembuild/remotemanager"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"text/template"
	"time"

	"github.com/cloudfoundry-incubator/stembuild/test/helpers"

	"github.com/masterzen/winrm"

	"github.com/concourse/pool-resource/out"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/vmware/govmomi/govc/cli"
	_ "github.com/vmware/govmomi/govc/importx"
	_ "github.com/vmware/govmomi/govc/vm"
	_ "github.com/vmware/govmomi/govc/vm/guest"
	_ "github.com/vmware/govmomi/govc/vm/snapshot"

	"syscall"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
)

func TestConstruct(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Construct Suite")
}

const (
	NetworkGatewayVariable            = "NETWORK_GATEWAY"
	SubnetMaskVariable                = "SUBNET_MASK"
	OvaFileVariable                   = "OVA_FILE"
	VMNamePrefixVariable              = "VM_NAME_PREFIX"
	VMFolderVariable                  = "VM_FOLDER"
	VMUsernameVariable                = "VM_USERNAME"
	VMPasswordVariable                = "VM_PASSWORD"
	VMNetworkVariable                 = "GOVC_NETWORK"
	ExistingVmIPVariable              = "EXISTING_VM_IP"
	UserProvidedIPVariable            = "USER_PROVIDED_IP"
	LockPrivateKeyVariable            = "LOCK_PRIVATE_KEY"
	IPPoolGitURIVariable              = "IP_POOL_GIT_URI"
	IPPoolNameVariable                = "IP_POOL_NAME"
	OvaSourceS3RegionVariable         = "OVA_SOURCE_S3_REGION"
	OvaSourceS3BucketVariable         = "OVA_SOURCE_S3_BUCKET"
	OvaSourceS3FilenameVariable       = "OVA_SOURCE_S3_FILENAME"
	AwsAccessKeyVariable              = "AWS_ACCESS_KEY_ID"
	AwsSecretKeyVariable              = "AWS_SECRET_ACCESS_KEY"
	SkipCleanupVariable               = "SKIP_CLEANUP"
	vcenterFolderVariable             = "VM_FOLDER"
	vcenterAdminCredentialUrlVariable = "VCENTER_ADMIN_CREDENTIAL_URL"
	vcenterBaseURLVariable            = "VCENTER_BASE_URL"
	vcenterStembuildUsernameVariable  = "VCENTER_STEMBUILD_USER"
	vcenterStembuildPasswordVariable  = "VCENTER_STEMBUILD_PASSWORD"
	StembuildVersionVariable          = "STEMBUILD_VERSION"
	VmSnapshotName                    = "integration-test-snapshot"
	LoggedInVmIpVariable              = "LOGOUT_INTEGRATION_TEST_VM_IP"
	LoggedInVmIpathVariable           = "LOGOUT_INTEGRATION_TEST_VM_INVENTORY_PATH"
	LoggedInVmSnapshotName            = "logged-in"
)

var (
	conf                      config
	tmpDir                    string
	lockParentDir             string
	lockPool                  out.LockPool
	lockDir                   string
	stembuildExecutable       string
	existingVM                bool
	vcenterAdminCredentialUrl string
)

type config struct {
	TargetIP           string
	NetworkGateway     string
	SubnetMask         string
	VMUsername         string
	VMPassword         string
	VMName             string
	VMNetwork          string
	VCenterURL         string
	VCenterUsername    string
	VCenterPassword    string
	VMInventoryPath    string
	LoggedInVMIP       string
	LoggedInVMIpath    string
	LoggedInVMSnapshot string
}

func restoreSnapshot(vmIpath string, snapshotName string) {
	snapshotCommand := []string{
		"snapshot.revert",
		fmt.Sprintf("-vm.ipath=%s", vmIpath),
		fmt.Sprintf("-u=%s", vcenterAdminCredentialUrl),
		snapshotName,
	}
	fmt.Printf("Reverting VM Snapshot: %s", snapshotName)
	runIgnoringOutput(snapshotCommand)
}

func waitForVmToBeReady(vmIp string, vmUsername string, vmPassword string) {
	clientFactory := remotemanager.NewWinRmClientFactory(vmIp, vmUsername, vmPassword)
	rm := remotemanager.NewWinRM(vmIp, vmUsername, vmPassword, clientFactory)
	Expect(rm).ToNot(BeNil())

	vmReady := false
	for !vmReady {
		_, err := rm.ExecuteCommand("powershell.exe \"ls c:\\windows 1>$null\"")
		vmReady = err == nil
	}
}

var _ = BeforeEach(func() {
	restoreSnapshot(conf.VMInventoryPath, VmSnapshotName)

	waitForVmToBeReady(conf.TargetIP, conf.VMUsername, conf.VMPassword)
	time.Sleep(30 * time.Second)
})

func envMustExist(variableName string) string {
	result := os.Getenv(variableName)
	if result == "" {
		Fail(fmt.Sprintf("%s must be set", variableName))
	}

	return result
}

func envMustExistWithDescription(variableName, description string) string {
	result := os.Getenv(variableName)
	if result == "" {
		Fail(fmt.Sprintf("%s %s must be set", description, variableName))
	}

	return result
}

func claimAvailableIP() string {
	lockPrivateKey := envMustExist(LockPrivateKeyVariable)
	ipPoolGitURI := envMustExist(IPPoolGitURIVariable)
	ipPoolName := envMustExist(IPPoolNameVariable)

	lockParentDir = filepath.Join(tmpDir, "lockParent")
	err := os.MkdirAll(lockParentDir, 0755)
	Expect(err).NotTo(HaveOccurred())

	keyFile, err := ioutil.TempFile(lockParentDir, "keyfile")
	Expect(err).NotTo(HaveOccurred())
	_, _ = keyFile.Write([]byte(lockPrivateKey))
	_ = keyFile.Chmod(0600)

	err = exec.Command("ssh-add", keyFile.Name()).Run()
	Expect(err).NotTo(HaveOccurred())

	poolSource := out.Source{
		URI:        ipPoolGitURI,
		Branch:     "master",
		Pool:       ipPoolName,
		RetryDelay: 5 * time.Second,
	}

	buffer := bytes.Buffer{}
	writer := bufio.NewWriter(&buffer)

	lockPool = out.NewLockPool(poolSource, writer)

	ip, _, err := lockPool.AcquireLock()
	Expect(err).NotTo(HaveOccurred())
	Expect(ip).NotTo(Equal(""))

	lockDir, err = ioutil.TempDir("", "acquired-lock")
	Expect(err).NotTo(HaveOccurred())
	err = ioutil.WriteFile(filepath.Join(lockDir, "name"), []byte(ip), os.ModePerm)
	Expect(err).NotTo(HaveOccurred())

	return ip
}

var _ = SynchronizedBeforeSuite(func() []byte {
	existingVM = false
	var err error
	var targetIP string

	stembuildVersion := envMustExist(StembuildVersionVariable)
	stembuildExecutable, err = helpers.BuildStembuild(stembuildVersion)
	Expect(err).NotTo(HaveOccurred())

	vmUsername := envMustExist(VMUsernameVariable)
	vmPassword := envMustExist(VMPasswordVariable)
	existingVMIP := os.Getenv(ExistingVmIPVariable)
	userProvidedIP := os.Getenv(UserProvidedIPVariable)
	loggedInVmIp := envMustExist(LoggedInVmIpVariable)
	loggedInVmInventoryPath := envMustExist(LoggedInVmIpathVariable)
	loggedInVmSnapshot := LoggedInVmSnapshotName
	vCenterUrl := envMustExist(vcenterBaseURLVariable)
	vcenterFolder := envMustExist(vcenterFolderVariable)
	vmNamePrefix := envMustExist(VMNamePrefixVariable)
	vcenterAdminCredentialUrl = envMustExist(vcenterAdminCredentialUrlVariable)

	vCenterStembuildUser := envMustExist(vcenterStembuildUsernameVariable)
	vCenterStembuildPassword := envMustExist(vcenterStembuildPasswordVariable)

	wd, err := os.Getwd()
	Expect(err).NotTo(HaveOccurred())
	tmpDir, err = ioutil.TempDir(wd, "construct-integration")
	Expect(err).NotTo(HaveOccurred())

	err = os.MkdirAll(tmpDir, 0755)
	Expect(err).NotTo(HaveOccurred())

	conf = config{
		TargetIP:           existingVMIP,
		VMUsername:         vmUsername,
		VMPassword:         vmPassword,
		VCenterURL:         vCenterUrl,
		VCenterUsername:    vCenterStembuildUser,
		VCenterPassword:    vCenterStembuildPassword,
		LoggedInVMIP:       loggedInVmIp,
		LoggedInVMIpath:    loggedInVmInventoryPath,
		LoggedInVMSnapshot: loggedInVmSnapshot,
	}

	if existingVMIP == "" {
		if userProvidedIP != "" {
			targetIP = userProvidedIP
			fmt.Printf("Creating VM with IP: %s\n", targetIP)
		} else {
			fmt.Println("Finding available IP...")
			targetIP = claimAvailableIP()
		}
		createVMWithIP(targetIP, vmNamePrefix, vcenterFolder)

		createVMSnapshot(VmSnapshotName)
	} else {
		existingVM = true
		targetIP = existingVMIP
		fmt.Printf("Using existing VM with IP: %s\n", targetIP)

		vmNameSuffix := strings.Split(targetIP, ".")[3]
		vmName := fmt.Sprintf("%s%s", vmNamePrefix, vmNameSuffix)
		conf.VMName = vmName
		vmInventoryPath := strings.Join([]string{vcenterFolder, vmName}, "/")
		conf.VMInventoryPath = vmInventoryPath

		powerOnVM()
	}
	fmt.Println("Attempting to connect to VM")
	endpoint := winrm.NewEndpoint(targetIP, 5985, false, true, nil, nil, nil, 0)
	client, err := winrm.NewClient(endpoint, vmUsername, vmPassword)
	Expect(err).NotTo(HaveOccurred())

	var shell *winrm.Shell
	Eventually(func() error {
		shell, err = client.CreateShell()
		return err
	}, 5*time.Minute).Should(BeNil())
	_ = shell.Close()
	fmt.Println("Successfully connected to VM")

	return nil
}, func(_ []byte) {
})

func createVMSnapshot(snapshotName string) {
	snapshotCommand := []string{
		"snapshot.create",
		fmt.Sprintf("-vm.ipath=%s", conf.VMInventoryPath),
		fmt.Sprintf("-u=%s", vcenterAdminCredentialUrl),
		snapshotName,
	}
	fmt.Printf("Creating VM Snapshot: %s\n", snapshotName)
	// is blocking
	runIgnoringOutput(snapshotCommand)
	time.Sleep(30 * time.Second)
}

func powerOnVM() {
	powerOnCommand := []string{
		"vm.power",
		fmt.Sprintf("-vm.ipath=%s", conf.VMInventoryPath),
		fmt.Sprintf("-u=%s", vcenterAdminCredentialUrl),
		fmt.Sprintf("-on"),
	}
	runIgnoringOutput(powerOnCommand)
}

func createVMWithIP(targetIP, vmNamePrefix, vcenterFolder string) {
	failureDescription := fmt.Sprintf("when creating a VM, because %s isn't set", ExistingVmIPVariable)

	ovaFile := validatedOVALocation()

	vmFolder := envMustExistWithDescription(VMFolderVariable, failureDescription)
	conf.NetworkGateway = envMustExistWithDescription(NetworkGatewayVariable, failureDescription)
	conf.SubnetMask = envMustExistWithDescription(SubnetMaskVariable, failureDescription)
	conf.VMNetwork = envMustExistWithDescription(VMNetworkVariable, failureDescription)

	conf.TargetIP = targetIP
	fmt.Printf("Target ip is %s\n", targetIP)

	vmNameSuffix := strings.Split(targetIP, ".")[3]
	vmName := fmt.Sprintf("%s%s", vmNamePrefix, vmNameSuffix)
	conf.VMName = vmName

	conf.VMInventoryPath = strings.Join([]string{vcenterFolder, vmName}, "/")

	templateFile, err := filepath.Abs("assets/ova_options.json.template")
	Expect(err).NotTo(HaveOccurred())
	tmpl, err := template.New("ova_options.json.template").ParseFiles(templateFile)
	Expect(err).ToNot(HaveOccurred())

	tmpDir, err := ioutil.TempDir("", "construct-test")
	Expect(err).NotTo(HaveOccurred())

	optionsFile, err := ioutil.TempFile(tmpDir, "ova_options*.json")
	Expect(err).NotTo(HaveOccurred())

	err = tmpl.Execute(optionsFile, conf)
	Expect(err).NotTo(HaveOccurred())

	opts := []string{
		"import.ova",
		fmt.Sprintf("--options=%s", optionsFile.Name()),
		fmt.Sprintf("--name=%s", vmName),
		fmt.Sprintf("--folder=%s", vmFolder),
		fmt.Sprintf("-u=%s", vcenterAdminCredentialUrl),
		ovaFile,
	}

	fmt.Printf("Opts are %s", opts)

	exitCode := cli.Run(opts)
	Expect(exitCode).To(BeZero())

}

func validatedOVALocation() string {
	providedLocation := os.Getenv(OvaFileVariable)
	if providedLocation != "" {
		_, err := os.Stat(providedLocation)
		Expect(err).NotTo(
			HaveOccurred(),
			fmt.Sprintf("OVA File doesn't exist at %s, as configured by %s", providedLocation, OvaFileVariable),
		)

		return providedLocation
	}

	failureDescription := fmt.Sprintf(
		"when creating a VM because %s isn't set %s isn't set will attempt to download from an S3 source,",
		ExistingVmIPVariable, OvaFileVariable,
	)

	s3Region := envMustExistWithDescription(OvaSourceS3RegionVariable, failureDescription)
	s3Bucket := envMustExistWithDescription(OvaSourceS3BucketVariable, failureDescription)
	s3Filename := envMustExistWithDescription(OvaSourceS3FilenameVariable, failureDescription)
	envMustExistWithDescription(AwsAccessKeyVariable, failureDescription)
	envMustExistWithDescription(AwsSecretKeyVariable, failureDescription)

	fmt.Printf(
		"%s not set, attempting to download from %s/%s in S3 region %s\n",
		OvaFileVariable,
		s3Bucket,
		s3Filename,
		s3Region,
	)

	ovaFile, err := ioutil.TempFile(tmpDir, "stembuild-construct-test.ova")
	defer ovaFile.Close()
	Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("%s unable to create temporary OVA file", failureDescription))

	sess, err := session.NewSession(
		&aws.Config{
			Region: aws.String(s3Region),
		},
	)

	Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("%s unable to create aws s3 session", failureDescription))

	s3Downloader := s3manager.NewDownloader(sess)
	_, err = s3Downloader.Download(
		ovaFile,
		&s3.GetObjectInput{
			Bucket: aws.String(s3Bucket),
			Key:    aws.String(s3Filename),
		},
	)

	Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("%s failed to download test OVA", failureDescription))
	fmt.Printf("Downloaded OVA file to %s\n", ovaFile.Name())

	return ovaFile.Name()
}

func runIgnoringOutput(args []string) int {
	oldStderr := os.Stderr
	oldStdout := os.Stdout

	_, w, _ := os.Pipe()

	defer w.Close()

	os.Stderr = w
	os.Stdout = w

	os.Stderr = os.NewFile(uintptr(syscall.Stderr), "/dev/null")

	exitCode := cli.Run(args)

	os.Stderr = oldStderr
	os.Stdout = oldStdout

	return exitCode
}

var _ = SynchronizedAfterSuite(func() {
	skipCleanup := strings.ToUpper(os.Getenv(SkipCleanupVariable))

	if !existingVM && skipCleanup != "TRUE" {
		deleteCommand := []string{
			"vm.destroy",
			fmt.Sprintf("-vm.ipath=%s", conf.VMInventoryPath),
			fmt.Sprintf("-u=%s", vcenterAdminCredentialUrl),
		}
		Eventually(func() int {
			return cli.Run(deleteCommand)
		}, 3*time.Minute, 10*time.Second).Should(BeZero())
		fmt.Println("VM destroyed")
		if lockDir != "" {
			_, _, err := lockPool.ReleaseLock(lockDir)
			Expect(err).NotTo(HaveOccurred())

			childItems, err := ioutil.ReadDir(lockParentDir)
			Expect(err).NotTo(HaveOccurred())

			for _, item := range childItems {
				if item.IsDir() && strings.HasPrefix(filepath.Base(item.Name()), "pool-resource") {
					fmt.Printf("Cleaning up temporary pool resource %s\n", item.Name())
					_ = os.RemoveAll(item.Name())
				}
			}
		}
	}

	_ = os.RemoveAll(tmpDir)
}, func() {
})
