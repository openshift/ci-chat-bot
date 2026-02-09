// Package orgdatacore provides high-performance organizational data access
// with O(1) lookups for employee, team, organization, pillar, and team group queries.
//
// All organizational relationships are pre-computed during indexing - no tree traversals at query time.
//
// # Quick Start
//
//	service := orgdatacore.NewService()
//	source, _ := orgdatacore.NewGCSDataSourceWithSDK(ctx, "bucket", "path/to/data.json")
//	defer source.Close()
//
//	service.LoadFromDataSource(ctx, source)
//
//	emp := service.GetEmployeeByUID("jsmith")
//	teams := service.GetTeamsForUID("jsmith")
//
// # Build Tags
//
// GCS support requires the "gcs" build tag to avoid forcing heavy cloud dependencies on all consumers:
//
//	go build -tags gcs
//
// Without this tag, GCS functions return [ErrGCSNotEnabled].
//
// # Error Handling
//
// Use [errors.Is] and [errors.As] with the sentinel errors:
//
//	if errors.Is(err, orgdatacore.ErrNotFound) { ... }
//
//	var loadErr *orgdatacore.LoadError
//	if errors.As(err, &loadErr) {
//	    fmt.Println("Source:", loadErr.Source)
//	}
//
// # Iterators (Go 1.23+)
//
// Memory-efficient iteration over large datasets:
//
//	for emp := range service.AllEmployees() {
//	    fmt.Println(emp.FullName)
//	}
//
// # Thread Safety
//
// All [Service] methods are safe for concurrent use.
package orgdatacore
