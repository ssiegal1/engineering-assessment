package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"googlemaps.github.io/maps"
)

func TestFileRead(t *testing.T) {
	foodTrucks, err := readFoodTruckData("Mobile_Food_Facility_Permit.csv")

	// Check for success
	if err != nil {
		t.Errorf("readFoodTruckData returned error %v", err)
	}

	// Check for correct number of rows read
	assert.Equal(t, 60, len(foodTrucks), "readFoodTruckData returned correct number of rows")

}

// Mock the Google Maps API with a dummy distanceMatrixRetriever
type distanceMatrixRetrieverMock struct{}

// DistanceMatrix: Keep it simple by always returning two within-range results
func (d distanceMatrixRetrieverMock) DistanceMatrix(ctx context.Context, r *maps.DistanceMatrixRequest) (*maps.DistanceMatrixResponse, error) {
	return &maps.DistanceMatrixResponse{
		OriginAddresses:      []string{"100 Spear St, San Francisco, CA 94105, USA"},
		DestinationAddresses: []string{"540 Howard St, San Francisco, CA 94105, USA", "329 Brannan St, San Francisco, CA 94107, USA"},
		Rows: []maps.DistanceMatrixElementsRow{
			{
				Elements: []*maps.DistanceMatrixElement{
					{
						Status:            "OK",
						Duration:          551000000000,
						DurationInTraffic: 0,
						Distance:          maps.Distance{HumanReadable: "0.7 km", Meters: 712},
					},
					{
						Status:            "OK",
						Duration:          1271000000000,
						DurationInTraffic: 0,
						Distance:          maps.Distance{HumanReadable: "1.6 km", Meters: 145},
					},
				},
			},
		},
	}, nil
}

// TestSearch validates that search parameters are applied as expected
func TestSearch(t *testing.T) {
	var tests = []struct {
		search string
		lat    float64
		lon    float64
		want   int
		desc   string
	}{
		{"", 0.0, 0.0, 60, "search with no parameters"},             // default
		{"pretzel", 0.0, 0.0, 4, "search with valid search term"},   // with search term
		{"XXX", 0.0, 0.0, 0, "search with invalid search term"},     // with unfindable search term
		{"", 37.7921505484, -122.393999, 6, "search with distance"}, // with walking distance
	}

	foodTrucks, err := readFoodTruckData("Mobile_Food_Facility_Permit.csv")
	if err != nil {
		t.Errorf("readFoodTruckData returned error %v", err)
	}
	var retriever distanceMatrixRetrieverMock

	for _, test := range tests {
		results := searchFoodTrucks(foodTrucks, retriever, false, test.search, test.lat, test.lon, false)
		assert.Equal(t, test.want, len(results), "got expected number of results for "+test.desc)
	}
}

// TestSort validates that the newest-first sort flag is correctly applied
func TestSort(t *testing.T) {
	foodTrucks, err := readFoodTruckData("Mobile_Food_Facility_Permit.csv")
	if err != nil {
		t.Errorf("readFoodTruckData returned error %v", err)
	}
	var retriever distanceMatrixRetrieverMock
	results := searchFoodTrucks(foodTrucks, retriever, false, "", 0.0, 0.0, true)
	assert.Equal(t, 60, len(results), "searchFoodTrucks with only sorting enabled returns all results")

	for i := 0; i < 59; i++ {
		assert.GreaterOrEqual(t, results[i].Received, results[i+1].Received, "searchFoodTrucks returns sorted results")
	}
}
