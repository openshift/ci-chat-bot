package orgdatacore

import (
	"testing"
)

func TestNewService(t *testing.T) {
	service := NewService()
	if service == nil {
		t.Fatal("NewService() returned nil")
	}
}

func TestServiceInterface(t *testing.T) {
	// This test ensures the Service implements ServiceInterface
	var _ ServiceInterface = (*Service)(nil)
}

func TestGetVersion(t *testing.T) {
	service := NewService()
	version := service.GetVersion()

	// Version should be zero value initially
	if version.LoadTime.IsZero() == false {
		t.Error("Expected zero time for initial version")
	}
}
