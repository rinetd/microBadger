package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	website "github.com/allentechnology/website"
	"github.com/blang/semver"
	"github.com/vharitonsky/iniflags"
	"html/template"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type mbSlice []*microBadge

func (m mbSlice) TrimWhiteSpace(text string) string {
	newString := strings.Replace(text, " ", "-", -1)
	newString = strings.Replace(newString, "(", "", -1)
	newString = strings.Replace(newString, ")", "", -1)
	return newString
}

var funcMap = template.FuncMap{
	"itemSum": func(m map[string]mbSlice) int {
		sum := 0
		for _, v := range m {
			sum += len(v)
		}
		return sum
	},
	"showPresets": func() []string {
		presetFiles := make([]string, 0)
		files, err := ioutil.ReadDir(appDir)
		if err != nil {
			return presetFiles
		}

		for _, v := range files {
			if strings.Contains(v.Name(), "preset-") {
				currentName := v.Name()
				currentName = strings.Replace(currentName, "preset-", "", -1)
				currentName = strings.Replace(currentName, ".mb", "", -1)
				presetFiles = append(presetFiles, currentName)
			}
		}
		return presetFiles
	},
	"checkUpdate": func() bool {
		return needToUpdate
	},
	"getLatestVersion": func() string {
		return latestVersion
	},
	"getVersion": func() string {
		return VERSION
	},
	"getOs": func() string {
		result := runtime.GOOS
		if result == "darwin" {
			return "osx"
		}
		return result
	},
	"getArch": func() string {
		result := runtime.GOARCH
		switch result {
		case "386":
			result = "32bit"
		case "amd64":
			result = "64bit"
		default:
		}
		if runtime.GOOS == "windows" {
			result += ".exe"
		}
		return result
	},
	"getPresets": func() []string {
		fileList, err := ioutil.ReadDir(appDir)
		presetList = make([]string, 0)
		if err != nil {
			return presetList
		}
		for _, file := range fileList {
			if strings.HasPrefix(file.Name(), "preset-") {
				presetList = append(presetList, file.Name())
			}
		}
		for i, v := range presetList {
			presetList[i] = strings.TrimPrefix(strings.TrimSuffix(v, ".mb"), "preset-")
		}
		return presetList
	},
}

var (
	slotMap          = map[string]*slot{}
	categoryMap      = map[string]mbSlice{}
	microBadgeMap    = map[string]*microBadge{}
	tmpMicroBadgeMap = map[string]*microBadge{}
	presetList       = make([]string, 0)
	presetChan       = make(chan bool)
	client           *http.Client
)

type notification []string

func (n *notification) notify(message string) {
	if len(notifications) > 50 {
		notifications = notifications[:len(notifications)-1]
	}
	currentTime := time.Now().Format("2006-01-02 15:04:05 ")
	*n = append(notification{currentTime + ": " + message}, *n...)
}

var (
	notifications = make(notification, 0)
)

var (
	loginReady        = make(chan bool)
	usingSelectedFile = make(chan bool, 1)
)

var (
	username = flag.String("username", "", "The boardgamegeek.com username used to log into the site")
	password = flag.String("password", "", "The boardgamegeek.com password associated with the given username")
	version  = flag.Bool("version", false, "Print the executable version to the screen")
	interval = flag.Int("interval", 1, "The interval between randomizations in minutes")
)

var (
	appDir        = ""
	runtimeOS     = ""
	latestVersion = ""
	needToUpdate  = false
	listenAddress = "localhost:8080"
)

func init() {

	currentUser, err := user.Current()
	if err != nil {
		log.Fatal(err)
	}
	latestVersion = checkForUpdates()
	needToUpdate = compareVersions(VERSION, latestVersion)
	runtimeOS = runtime.GOOS
	switch runtime.GOOS {
	case "linux":
		appDir = filepath.Join(currentUser.HomeDir, "/.microBadger")
	case "darwin":
		appDir = filepath.Join(currentUser.HomeDir, "/.microBadger")
	case "windows":
		appDir = filepath.Join(currentUser.HomeDir, "/microBadger")
	}
	err = os.MkdirAll(appDir, os.ModePerm)
	if err != nil {
		log.Fatal(err)
	}
	go cyclePresets([]string{})
}

func main() {
	iniflags.Parse()
	if *version {
		fmt.Println(VERSION)
		os.Exit(0)
	}
	loadMicroBadgesFromFile("selected.mb")
	categoryMap = getCategories()
	go webServer()

	var err error

	localURL := "http://" + listenAddress
	switch runtimeOS {
	case "linux":
		err = exec.Command("xdg-open", localURL).Start()
	case "darwin":
		err = exec.Command("open", localURL).Start()
	case "windows":
		err = exec.Command("cmd", "/C", "start", localURL).Start()
	default:
		err = fmt.Errorf("unsupported platform")
		log.Fatal(err)
	}
	if err != nil {
		fmt.Println("Failed to open browser. Navigate to", localURL, "on your preferred web browser.")
	}
	fmt.Println("MicroBadger version ", VERSION)
	fmt.Println("To use microBadger, navigate to", localURL, "in any web browser.")
	<-loginReady

	//	client = logIntoBGG()
	//	loggedIn := true
	for {
		// if !loggedIn {
		// 	client = logIntoBGG()
		// }
		latestVersion = checkForUpdates()
		needToUpdate = compareVersions(VERSION, latestVersion)
		notifications.notify("Attempting to randomize badges: ")
		err := getMicroBadges(client)
		if err != nil {
			notifications.notify("Failed")
			notifications.notify(err.Error())
			time.Sleep(10 * time.Second)
			continue
		}
		randomizeBadges()
		time.Sleep(time.Duration(*interval) * time.Minute)
	}
}

func loadMicroBadgesFromFile(file string) {
	for {
		fileName := filepath.Join(appDir, file)
		if _, err := os.Stat(fileName); err == nil {
			usingSelectedFile <- true
			inFile, err := os.Open(fileName)
			if err != nil {
				notifications.notify("Error opening file: " + err.Error())
				<-usingSelectedFile
				break
			}
			defer inFile.Close()
			selectedBytes, err := ioutil.ReadAll(inFile)
			if err != nil {
				notifications.notify("Error reading file: " + err.Error())
				<-usingSelectedFile
				break
			}
			tmpMicroBadgeMap = map[string]*microBadge{}
			microBadgeMap = map[string]*microBadge{}
			err = json.Unmarshal(selectedBytes, &tmpMicroBadgeMap)
			if err != nil {
				notifications.notify("Error in file format: " + err.Error())
				<-usingSelectedFile
				break
			}
			<-usingSelectedFile
			microBadgeMap = tmpMicroBadgeMap
		}
		break
	}
	availableSlots := make(map[string][]string)
	for _, v := range microBadgeMap {

		for i, sel := range v.Selected {
			index := fmt.Sprintf("%d", i+1)
			if sel {
				availableSlots[index] = append(availableSlots[index], v.Id)
			}
		}
	}
	submitCheckedMicroBadges(availableSlots)
}

func compareVersions(curVer, newVer string) bool {
	currentVersion, err := semver.Make(curVer)
	if err != nil {
		return false
	}
	newVersion, err := semver.Make(newVer)
	if err != nil {
		return false
	}
	result := currentVersion.Compare(newVersion)
	if result < 0 {
		return true
	}
	return false
}

func checkForUpdates() (newVersion string) {
	url := "http://github.com/allentechnology/microBadger/releases/latest"

	resp, err := http.Get(url)
	if err != nil {
		newVersion = VERSION
		return
	}

	newVersion = getVersionFromURL(resp.Request.URL.String())
	return
}

func getVersionFromURL(url string) string {
	stringSlice := strings.Split(url, "/")
	return stringSlice[len(stringSlice)-1]
}

func randomizeBadges() {
	badgeList := getRandomBadges()
	updateSuccess := make([]bool, len(badgeList))
	var err error
	for i, v := range badgeList {
		err = assignSlot(v.Id, fmt.Sprintf("%d", i+1), client)
		if err != nil {
			//			fmt.Println("Error assigning slot ", i+1, ": ", err.Error())
			updateSuccess[i] = false
			//	loggedIn = false
		} else {
			updateSuccess[i] = true
		}

	}
	updateMessage := "Slots "
	slotUpdated := false
	for i, v := range updateSuccess {
		if v {
			updateMessage += fmt.Sprintf("%d ", i+1)
			slotUpdated = true
		}
	}
	if slotUpdated {
		updateMessage += "updated successfully"
	} else {
		updateMessage += "not updated"
	}
	notifications.notify(updateMessage)
}

func cyclePresets(selectedPresets []string) {
	if len(selectedPresets) < 1 {
		<-presetChan

	} else {
		for {
			for _, v := range selectedPresets {
				select {
				case <-presetChan:
					return
				default:
					notifications.notify("loading " + v + " preset")
					loadMicroBadgesFromFile("preset-" + v + ".mb")

				}
				time.Sleep(time.Duration(*interval)*time.Minute + time.Second)
			}
		}
	}
}

func webServer() {
	//Web server here
	http.HandleFunc("/", rootHandler)
	http.HandleFunc("/slot/", slotHandler)
	http.HandleFunc("/slotSubmit", slotSubmitHandler)
	http.HandleFunc("/login", loginHandler)
	http.HandleFunc("/setInterval", setIntervalHandler)
	http.HandleFunc("/randomize", randomizeHandler)
	http.HandleFunc("/notification", notificationHandler)
	http.HandleFunc("/quit", quitHandler)
	http.HandleFunc("/test", testHandler)
	http.HandleFunc("/header", headerHandler)
	http.HandleFunc("/savePreset", savePresetHandler)
	http.HandleFunc("/loadPreset", loadPresetHandler)
	http.HandleFunc("/notify", notifyHandler)
	serverErr := http.ListenAndServe(listenAddress, nil)

	if serverErr != nil {
		os.Exit(0)
	}

}

func notifyHandler(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	currentNotification := r.Form["notification"]
	for _, v := range currentNotification {
		notifications.notify(v)
	}
	http.Redirect(w, r, "http://"+listenAddress, http.StatusSeeOther)
}

func savePresetHandler(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	presetName := r.Form["preset-name"]
	var err error = nil
	if len(presetName) > 0 {
		if len(presetName[0]) > 0 {
			fileName := "preset-" + presetName[0] + ".mb"
			writeMapToFile(fileName, microBadgeMap)
		} else {
			err = errors.New("Preset name not provided")
		}
	} else {
		err = errors.New("Preset name not provided")
	}

	if err != nil {
		//Preset name not provided
		http.Error(w, err.Error(), http.StatusNotFound)
	}
}

func loadPresetHandler(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	presetNames := r.Form["preset"]
	requestedPresets := make([]string, 0)
	for _, v := range presetNames {
		currentPreset := v
		for _, validPreset := range presetList {
			if currentPreset == validPreset {
				requestedPresets = append(requestedPresets, currentPreset)
			}
		}
	}
	if len(requestedPresets) > 0 {
		presetChan <- true
		go cyclePresets(requestedPresets)
		http.Redirect(w, r, "http://"+listenAddress, http.StatusSeeOther)
		return
	}

	http.Error(w, "The requested preset does not exist", http.StatusNotFound)
}

func notificationHandler(w http.ResponseWriter, r *http.Request) {
	notificationPage := `
<html>
<head>
<meta http-equiv="refresh" content="5" />
</head>
<body>
{{range .}}
{{.}}</br></br>
{{end}}
</body>
</html>
`
	tmpl, err := template.New("").Parse(notificationPage)

	if err != nil {
		fmt.Fprintf(w, "error: "+err.Error())
	}
	err = tmpl.Execute(w, notifications)
	if err != nil {
		fmt.Fprintf(w, "error: "+err.Error())
	}

}
func quitHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprint(w, "<html><body><h3>Exiting microBadger</h3><p>Thank you for using this application</p></body></html>")
	go func() {
		time.Sleep(5 * time.Second)
		os.Exit(0)
	}()
}
func randomizeHandler(w http.ResponseWriter, r *http.Request) {
	randomizeBadges()
}

func testHandler(w http.ResponseWriter, r *http.Request) {
	testString := `
<html>
<body>
{{range $index,$preset := showPresets}}
{{.}}<br />
{{end}}
</body>
</html>
`
	tmpl, err := template.New("").Funcs(funcMap).Parse(testString)

	if err != nil {
		fmt.Fprintf(w, "error: "+err.Error())
	}
	err = tmpl.Execute(w, nil)
	if err != nil {
		fmt.Fprintf(w, "error: "+err.Error())
	}

}

func headerHandler(w http.ResponseWriter, r *http.Request) {
	header, err := Asset("logos/microBadger_headert.png")
	if err == nil {
		w.Write(header)
	}
}

func setIntervalHandler(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	intervalSlice := r.Form["interval"]

	if len(intervalSlice) > 0 {
		formInterval, err := strconv.Atoi(intervalSlice[0])
		if err != nil {
			return
		}
		*interval = formInterval
	}
	return
}

func rootHandler(w http.ResponseWriter, r *http.Request) {
	//	tmpl, err := template.ParseFiles("webpage.html")
	// logo, err := Asset("logos/microBadger_headert.png")
	// if err == nil {
	// 	w.Write(logo)
	// }
	webpage, err := Asset("webpage.html")
	if err == nil {
		tmpl, err := template.New("").Funcs(funcMap).Parse(string(webpage))
		if err != nil {
			fmt.Fprintf(w, "error: "+err.Error())
		}
		err = tmpl.Execute(w, categoryMap)
		if err != nil {
			fmt.Fprintf(w, "error: "+err.Error())
		}
	} else {
		fmt.Fprintf(w, "error loading webpage")
	}
}

func slotSubmitHandler(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	formSlots := make(map[string][]string)
	formSlots["1"] = r.Form["slot1"]
	formSlots["2"] = r.Form["slot2"]
	formSlots["3"] = r.Form["slot3"]
	formSlots["4"] = r.Form["slot4"]
	formSlots["5"] = r.Form["slot5"]
	submitCheckedMicroBadges(formSlots)
}

func submitCheckedMicroBadges(formSlots map[string][]string) {
	mbSelectedMap := make(map[string][]bool)
	for i := 1; i < 6; i++ {
		slotID := fmt.Sprintf("%d", i)
		if s, ok := slotMap[slotID]; ok {
			s.AvailableBadges = map[string]*microBadge{}
			for _, v := range formSlots[slotID] {
				if mb, ok := microBadgeMap[v]; ok {
					if mbSelected, ok := mbSelectedMap[v]; ok {
						mbSelected[i-1] = true
					} else {
						mbSelectedMap[v] = make([]bool, 5)
						mbSelectedMap[v][i-1] = true
					}
					//					mb.Selected[i] = true
					s.AvailableBadges[v] = mb
				}
			}
		} else {
			newMap := make(map[string]*microBadge)
			slotMap[slotID] = &slot{Id: slotID, AvailableBadges: newMap}
			for _, v := range formSlots[slotID] {
				if mb, ok := microBadgeMap[v]; ok {
					if mbSelected, ok := mbSelectedMap[v]; ok {
						mbSelected[i-1] = true
					} else {
						mbSelectedMap[v] = make([]bool, 5)
						mbSelectedMap[v][i-1] = true
					}
					// mb.Selected[i] = true
					slotMap[slotID].AvailableBadges[v] = mb
				}
			}

		}
	}
	selectedMicroBadges := make(map[string]*microBadge)

	for key, mb := range microBadgeMap {
		if mbSelected, ok := mbSelectedMap[key]; ok {
			for i, sel := range mbSelected {
				mb.Selected[i] = sel
			}
			selectedMicroBadges[mb.Id] = mb
		} else {
			for i, _ := range mb.Selected {
				mb.Selected[i] = false
			}
		}
	}

	usingSelectedFile <- true
	fileName := "selected.mb"
	writeMapToFile(fileName, selectedMicroBadges)
	<-usingSelectedFile
}

func writeMapToFile(fileName string, givenMap interface{}) {

	toWritetoFile, err := json.Marshal(givenMap)
	if err != nil {
		notifications.notify("Error opening file: " + err.Error())
		return
	}
	outFile, err := os.Create(filepath.Join(appDir, fileName))
	if err != nil {
		notifications.notify("Error saving selections to file: " + err.Error())
		return
	}
	defer outFile.Close()
	outFile.Write(toWritetoFile)

}

func loginHandler(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	usernameSlice := r.Form["username"]
	passwordSlice := r.Form["password"]
	if len(usernameSlice) < 1 || len(passwordSlice) < 1 {
		//Print error to notification area
		return
	}
	*username = usernameSlice[0]
	*password = passwordSlice[0]

	var err error
	client, err = website.Login("https://boardgamegeek.com/login", *username, *password, 30*time.Second)

	if err != nil {
		notifications.notify(err.Error())
		if err.Error() == "Login failed" {
			return
		}
	} else {
		notifications.notify("Login successful. Reload page")
		loginReady <- true
	}

}

func slotHandler(w http.ResponseWriter, r *http.Request) {
	slotNumber := r.URL.Path[6:]
	if _, ok := slotMap[string(slotNumber)]; ok {
		if mb, ok := microBadgeMap[slotMap[string(slotNumber)].AssignedBadge]; ok {
			fmt.Fprintf(w, "<html><head><meta http-equiv='refresh' content='0; url=http:%s' /></head></html>", mb.ImgURL)
		}
	}

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

func getRandomBadges() []microBadge {

	badgeList := []microBadge{}
	for i := 1; i < 6; i++ {
		slotID := fmt.Sprintf("%d", i)
		if currentSlot, ok := slotMap[slotID]; ok {
			if len(currentSlot.AvailableBadges) > 0 {
				for _, mb := range currentSlot.AvailableBadges {
					mbAlreadyUsed := false
					for _, v := range badgeList {
						if v.Id == mb.Id {
							mbAlreadyUsed = true
							break
						}
					}
					if mbAlreadyUsed {
						continue
					}
					badgeList = append(badgeList, *mb)
					break
				}
			} else {
				badgeList = append(badgeList, microBadge{Id: ""})
			}
		} /*else {
			badgeList = append(badgeList, microBadge{})
		}*/
	}
	return badgeList
}

func assignSlot(id, slotNumber string, client *http.Client) error {
	var err error
	var resp *http.Response
	if id == "" {
		resp, err = client.PostForm("https://boardgamegeek.com/geekmicrobadge.php", url.Values{
			"slot":   {slotNumber},
			"ajax":   {"1"},
			"action": {"clearslot"},
		})

	} else {
		resp, err = client.PostForm("https://boardgamegeek.com/geekmicrobadge.php", url.Values{
			"badgeid": {id},
			"slot":    {slotNumber},
			"ajax":    {"1"},
			"action":  {"setslot"},
		})
	}
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
		slotMap[slotNumber] = &slot{Id: slotNumber, AssignedBadge: id}
	}
	return nil
}
