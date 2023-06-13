package main

import (
	"context"
	"encoding/csv"
	"fmt"
	"math"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/kr/pretty"
	"googlemaps.github.io/maps"
)

// foodTruckInfo - representation of a single row in the data set.
// Only some of the fields we read from the CSV file are useful for us.
// Comments include column number in the input CSV file
type foodTruckInfo struct {
	Name         string // 1: Name of food truck
	FacilityType string // 2: Truck or Push Cart, optional
	Address      string // 5
	Status       string // 10: We only want ones with APPROVED status
	FoodItems    string // 11: describes their offerings - search on this
	LatLon       string // 14,15 - transmogrify given lat/long into a string we can use in the Google distance API
	// Dayshours    string // 17: unfortunately this is very sparse, and not that searchable
	Received uint // 20: This will give us a "freshness" sort order
}

// distanceMatrixRetriever - interface to retrieve walking distance info
// This interface will make Google Maps API easier to mock in unit tests
type distanceMatrixRetriever interface {
	DistanceMatrix(ctx context.Context, r *maps.DistanceMatrixRequest) (*maps.DistanceMatrixResponse, error)
}

// Keep our master list of food trucks in a global variable so it persists between GET calls.
// This really should be a pointer to avoid an unnecessary copy of the data in readFoodTruckData().
var foodTrucks = []foodTruckInfo{}

// main - read in our dataset from a local CSV file, then
// expose it via a GET /foodtrucks API
// To use this module, GOOGLE_MAPS_API_KEY must be set in ENV
// Could that be done better? Probably, but I didn't want to expose a sensitive
// API key in code.
func main() {

	// At startup, read in food truck data
	var err error
	foodTrucks, err = readFoodTruckData("Mobile_Food_Facility_Permit.csv")
	if err != nil {
		panic(err)
	}

	router := gin.Default()
	router.GET("/foodtrucks", getFoodTrucks)

	router.Run("localhost:8080")

}

// getFoodTrucks responds with the list of all APPROVED food trucks as JSON.
// Optional parameters:
// - search term (search by cuisine etc)
// - lat/lon - if specified, limit results to food trucks within 1km of that location.
// - newest - sort by newest Received permit first
// - debug - print debugging info
//
// Error handling could be done better. Here I'm just bailing whenever anything goes
// wrong; the better approach would be to return a useful HTTP status and some sort of
// error message.
func getFoodTrucks(c *gin.Context) {

	params := c.Request.URL.Query()

	if params.Has("debug") {
		fmt.Printf("getFoodTrucks called with: %v\n", params)
	}
	// params: debug, search, lat/lon, newest
	var lat, lon float64
	var err error
	if params.Has("lat") && params.Has("lon") {
		lat, err = strconv.ParseFloat(params.Get("lat"), 64)
		if err != nil {
			panic(err)
		}
		lon, err = strconv.ParseFloat(params.Get("lon"), 64)
		if err != nil {
			panic(err)
		}
	}

	// Create an instance of the Google Maps API to retrieve
	// distance info for our list of food trucks
	// Arguably these might both (apiKey and distRetriever) should
	// be global variables so I don't have to initialize them with every search.
	apiKey := os.Getenv("GOOGLE_MAPS_API_KEY")
	if len(apiKey) == 0 {
		panic(fmt.Errorf("GOOGLE_MAPS_API_KEY env var needs to be set"))
	}
	distRetriever, err := maps.NewClient(maps.WithAPIKey(apiKey))
	if err != nil {
		panic(err)
	}

	results := searchFoodTrucks(foodTrucks, distRetriever, params.Has("debug"), params.Get("search"), lat, lon, params.Has("newest"))
	c.IndentedJSON(http.StatusOK, results)
}

// searchFoodTrucks searches/orders the items in searchList; options include specifying a searchTerm,
// a location via lat/lon (location search is performed by the distRetriever which limits the search to 1km from the
// specified lat/lon), and whether to sort the list by newest first. The debugging parameter
// just makes this more verbose.
func searchFoodTrucks(searchList []foodTruckInfo, distRetriever distanceMatrixRetriever,
	debugging bool, searchTerm string, lat float64, lon float64, newestFirst bool) []foodTruckInfo {

	var results []foodTruckInfo
	var err error

	if len(searchTerm) > 0 {
		if debugging {
			fmt.Println("searching for trucks serving \"" + searchTerm + "\"")
		}
		for _, truck := range searchList {
			if strings.Contains(strings.ToLower(truck.FoodItems), strings.ToLower(searchTerm)) {
				results = append(results, truck)
			}
		}
	} else {
		results = searchList
	}

	// If latitude AND longitude are both specified, search within walking distance
	// (I define walking distance as 1km)
	// We *should* give some sort of indication that we're
	// ignoring a lat without a lon, and vice versa. Since I'm not really dealing
	// with reporting errors in a useful way, though, that case will silently fail.
	if lat != 0 && lon != 0 {
		results, err = getWalkableTrucks(distRetriever, results, lat, lon, debugging)
		if err != nil {
			panic(err)
		}
	}

	if newestFirst {
		if debugging {
			fmt.Println("Prioritizing newest food trucks")
		}
		sort.Slice(results, func(i, j int) bool {
			return results[i].Received > results[j].Received
		})
	}
	return results
}

// readFoodTruckData accepts a file name and reads its rows into an array of foodTruckInfo.
// This is where we filter out any non-approved food trucks
func readFoodTruckData(filename string) ([]foodTruckInfo, error) {

	// Open CSV file
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	lines, err := csv.NewReader(f).ReadAll()
	if err != nil {
		return nil, err
	}

	var trucksInFile []foodTruckInfo
	// Loop through lines and turn into structured data
	// Line 0 is a "header" line so ignore it
	for _, line := range lines[1:] {
		recv, err := strconv.Atoi(line[20])
		if err != nil {
			return nil, err
		}
		info := foodTruckInfo{
			Name:         line[1],
			FacilityType: line[2],
			Address:      line[5],
			Status:       line[10],
			FoodItems:    line[11],
			LatLon:       fmt.Sprintf("%s,%s", line[14], line[15]), // Google maps API uses "lat,lon" format
			// Dayshours:    line[17], haven't figured out how to use this yet
			Received: uint(recv),
		}

		// We do not want to get food poisoning.
		// Let's start by filtering out any that aren't approved.
		if info.Status == "APPROVED" {
			trucksInFile = append(trucksInFile, info)
		}
	}

	return trucksInFile, nil
}

// getWalkableTrucks uses Google's DistanceMatrix API to determine which food trucks are within walking distance.
// Walking distance is defined as 1km.
func getWalkableTrucks(d distanceMatrixRetriever, allTrucks []foodTruckInfo, lat float64, lon float64, debugging bool) ([]foodTruckInfo, error) {

	// Google maps API only allows up to 25 destinations per request, so we need to chunk our requests
	maxDestinationsPerRequest := 25
	numRequests := int(math.Ceil(float64(len(allTrucks)) / float64(maxDestinationsPerRequest)))

	if debugging {
		fmt.Printf("Splitting list of length %d into %d batch requests\n", len(allTrucks), numRequests)
	}

	// We will always have one origin: set it up before entering the loop
	var origins []string
	origins = append(origins, fmt.Sprintf("%f,%f", lat, lon))
	var walkableTrucks []foodTruckInfo

	// For better performance, this could/should be threaded so requests are sent in parallel
	for i := 0; i < numRequests; i++ {

		var destinations []string
		startIdx := i * maxDestinationsPerRequest
		endIdx := int(math.Min(float64(len(allTrucks)), float64(startIdx+maxDestinationsPerRequest))) // kinda dumb there is no integer min

		for _, truck := range allTrucks[startIdx:endIdx] {
			destinations = append(destinations, truck.LatLon)
		}
		if debugging {
			fmt.Printf("Calling google maps API for indexes [%d,%d]\n", startIdx, endIdx)
			fmt.Printf("Origin: %v\n", origins)
			fmt.Printf("Destinations: %v\n", destinations)
		}

		// Google Distance Matrix API helps determine the most efficient travel routes between multiple possible origins
		// and destinations.
		r := &maps.DistanceMatrixRequest{
			Origins:       origins,
			Destinations:  destinations,
			Language:      "en",
			DepartureTime: "now",
			Mode:          maps.TravelModeWalking,
			Units:         maps.UnitsMetric,
		}

		resp, err := d.DistanceMatrix(context.Background(), r)
		if err != nil {
			panic(err)
		}

		if debugging {
			pretty.Println(resp)
		}

		// Only include distances under 1km in results (Supposedly that's an under-15min walk)
		for v, elem := range resp.Rows[0].Elements {
			if elem.Status == "OK" && elem.Distance.Meters < 1000 {
				walkableTrucks = append(walkableTrucks, allTrucks[startIdx+v])
			}
		}

	}

	return walkableTrucks, nil
}
