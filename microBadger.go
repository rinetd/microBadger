package main

import (
	"errors"
	"flag"
	"fmt"
	website "github.com/allentechnology/website"
	"github.com/vharitonsky/iniflags"
	"html/template"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"time"
)

var (
	slotMap          = map[string]slot{}
	categoryMap      = map[string][]*microBadge{}
	microBadgeMap    = map[string]*microBadge{}
	tmpMicroBadgeMap = map[string]*microBadge{}
)

var (
	username = flag.String("username", "", "The boardgamegeek.com username used to log into the site")
	password = flag.String("password", "", "The boardgamegeek.com password associated with the given username")
	version  = flag.Bool("version", false, "Print the executable version to the screen")
	interval = flag.Int("interval", 1, "The interval between randomizations in minutes")
)

var (
	appDir    = ""
	runtimeOS = ""
)

func init() {

	currentUser, err := user.Current()
	if err != nil {
		log.Fatal(err)
	}
	runtimeOS = runtime.GOOS
	switch runtime.GOOS {
	case "linux", "darwin":
		appDir = currentUser.HomeDir + ".microBadger"
	case "windows":
		appDir = currentUser.HomeDir + "microBadger"
	}
}

func main() {
	iniflags.Parse()
	if *version {
		fmt.Println(VERSION)
		os.Exit(0)
	}

	go webServer()

	var err error
	switch runtimeOS {
	case "linux":
		err = exec.Command("xdg-open", "http://localhost:6060/").Start()
	case "darwin":
		err = exec.Command("open", "http://localhost:6060/").Start()
	case "windows":
		err = exec.Command("cmd", "/C", "start", "http://localhost:6060/").Start()
	default:
		err = fmt.Errorf("unsupported platform")
		log.Fatal(err)
	}
	if err != nil {
		fmt.Println("Failed to open browser. Navigate to http://localhost/ on your preferred web browser.")
	}
	time.Sleep(1 * time.Second)
	for *username == "" || *password == "" {
		getUsername()
		if *username == "" {
			fmt.Println("Invalid username")
			continue
		} else if *password == "" {
			fmt.Println("Invalid password")
			continue
		}
	}
	var client *http.Client
	for {
		var err error
		client, err = website.Login("https://boardgamegeek.com/login", *username, *password, 30*time.Second)

		if err != nil {
			fmt.Println(err.Error())
			if err.Error() == "Login failed" {
				fmt.Println("Exiting microBadger")
				os.Exit(1)
			}
		} else {
			break
		}
		time.Sleep(10 * time.Minute)

	}

	//	client = logIntoBGG()
	//	loggedIn := true
	for {
		// if !loggedIn {
		// 	client = logIntoBGG()
		// }

		fmt.Print(time.Now().Format("2006-01-02 15:04:05 "))
		fmt.Print("Attempting to randomize badges: ")
		err := getMicroBadges(client)
		if err != nil {
			fmt.Println("Failed")
			fmt.Println(err.Error())
			time.Sleep(10 * time.Second)
			continue
		}
		badgeList := getRandomBadges(5)

		updateSuccess := make([]bool, len(badgeList))
		for i, v := range badgeList {
			err = assignSlot(v.Id, fmt.Sprintf("%d", i+1), client)
			if err != nil {
				// fmt.Println("Error assigning slot ", i+1, ": ", err.Error())
				updateSuccess[i] = false
				//	loggedIn = false
			} else {
				updateSuccess[i] = true
			}

		}
		fmt.Println()
		fmt.Print("Slots ")
		slotUpdated := false
		for i, v := range updateSuccess {
			if v {
				fmt.Printf("%d ", i+1)
				slotUpdated = true
			}
		}
		if slotUpdated {
			fmt.Println("updated successfully")
		} else {
			fmt.Println("not updated")
		}
		time.Sleep(time.Duration(*interval) * time.Minute)
	}
}

func webServer() {
	//Web server here
	http.HandleFunc("/", rootHandler)
	http.HandleFunc("/test", testHandler)
	serverErr := http.ListenAndServe(":6060", nil)

	if serverErr != nil {
		os.Exit(0)
	}

}

func rootHandler(w http.ResponseWriter, r *http.Request) {
	tmpl, err := template.ParseFiles("webpage.html")

	if err != nil {
		fmt.Fprintf(w, "error: "+err.Error())
	}
	err = tmpl.Execute(w, categoryMap)
	if err != nil {
		fmt.Fprintf(w, "error: "+err.Error())
	}
}

func testHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "%s", r.URL.Path[1:])
}

func logIntoBGG() (client *http.Client) {
	for {
		var err error
		client, err = website.Login("https://boardgamegeek.com/login", *username, *password, 30*time.Second)

		if err != nil {
			fmt.Println("BGG currently unavailable")
			//			log.Fatal(err)
		} else {
			return
		}
		time.Sleep(10 * time.Second)
	}
}

func getRandomBadges(numBadges int) []microBadge {
	i := 0
	badgeList := []microBadge{}
	for _, v := range microBadgeMap {
		if v.Category != "Contest (Official)" {
			badgeList = append(badgeList, *v)
			i++
		}
		if i >= numBadges {
			break
		}
	}
	return badgeList
}

func assignSlot(id, slotNumber string, client *http.Client) error {
	resp, err := client.PostForm("https://boardgamegeek.com/geekmicrobadge.php", url.Values{
		"badgeid": {id},
		"slot":    {slotNumber},
		"ajax":    {"1"},
		"action":  {"setslot"},
	})
	if err != nil {
		return err
	}
	data, err := ioutil.ReadAll(resp.Body)
	defer resp.Body.Close()
	if err != nil {
		return err
	}
	//Response if not logged in is 85 bytes long
	if len(data) < 86 {
		return errors.New("Invalid username or password. Restart microBadger and attempt to log in again.")
	}
	if givenSlot, ok := slotMap[slotNumber]; ok {
		givenSlot.AssignedBadge = id
	} else {
		slotMap[slotNumber] = slot{Id: slotNumber, AssignedBadge: id}
	}
	return nil
}
