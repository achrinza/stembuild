package iaas_clients_test

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestIaasClients(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "IaasClients Suite")
}
