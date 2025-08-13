package main

import (
	"fmt"
	"log"

	"github.com/openshift/ci-chat-bot/pkg/orgdata-core"
)

func main() {
	// Create a new service
	service := orgdatacore.NewService()

	fmt.Println("✅ Created new organizational data service")

	// Load data from JSON files
	err := service.LoadFromFiles([]string{"../../test-data/comprehensive_index_dump.json"})
	if err != nil {
		log.Printf("    Could not load test data: %v", err)
		log.Println("   This is expected if test data is not available")
		log.Println("   The service is still functional for demonstration")
	} else {
		fmt.Println("✅ Loaded organizational data")

		// Get version info
		version := service.GetVersion()
		fmt.Printf("📊 Data version: %d employees, %d orgs\n",
			version.EmployeeCount, version.OrgCount)

		// Example queries (these will work if test data is loaded)
		if emp := service.GetEmployeeBySlackID("USE4Y4UG5"); emp != nil {
			fmt.Printf("👤 Found employee: %s (%s)\n", emp.FullName, emp.JobTitle)
		}

		teams := service.GetTeamsForSlackID("USE4Y4UG5")
		if len(teams) > 0 {
			fmt.Printf("🏢 User is member of teams: %v\n", teams)
		}
	}

	fmt.Println("\nCore package is ready for use!")
	fmt.Println("   - Import: github.com/openshift/ci-chat-bot/pkg/orgdata-core")
	fmt.Println("   - Interface: orgdatacore.ServiceInterface")
	fmt.Println("   - Implementation: orgdatacore.Service")
}
