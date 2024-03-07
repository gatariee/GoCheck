package scanner

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	utils "github.com/gatariee/gocheck/utils"
)

const (
	ScanPath    = "C:\\Program Files (x86)\\Kaspersky Lab\\Kaspersky Security Cloud 21.3\\avp.com"
	AltScanPath = "C:\\Program Files\\Kaspersky Lab\\Kaspersky Security Cloud 21.3\\avp.com"
)

func Scan(file string, scanPath string) (string, error) {
	scanCmd := exec.Command(scanPath, "SCAN", file, "/i0")
	var out, stderr bytes.Buffer
	scanCmd.Stdout = &out
	scanCmd.Stderr = &stderr

	scanCmd.Run()

	output := out.String()
	return output, nil
}

func IsMalicious(output string) bool {
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if strings.Contains(line, "suspicion") {
			/* This might be a problem */
			return true
		}
	}
	return false
}

func GetSignature(output string) string {
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if strings.Contains(line, "HEUR:") {
			parts := strings.Fields(line)
			for _, part := range parts {
				if strings.Contains(part, "HEUR:") {
					return part
				}
			}
		}
	}

	return "No signature found"
}

func CheckIfExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func KasperskyRun(file string, scanPath string, debug bool) error {
	original_file, err := os.ReadFile(file)
	if err != nil {
		return err
	}

	start := time.Now()
	ticker := time.NewTicker(time.Duration(2 * float64(time.Second)))
	defer ticker.Stop()

	progressUpdates := make(chan Progress)
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ticker.C:
				progress, ok := <-progressUpdates
				if !ok {
					return
				}
				current := time.Since(start)
				utils.PrintErr(fmt.Sprintf("0x%X -> 0x%X - malicious: %t - %s", progress.Low, progress.High, progress.Malicious, current))
			case _, ok := <-progressUpdates:
				/* we don't want the scanner to wait for ticker.C to reopen, so we need to handle this case */
				if !ok {
					return
				}
			}
		}
	}()

	threat_names := make(chan string)
	threat_list := make([]string, 0)
	go func() {
		for {
			threat_name := <-threat_names
			threat_list = append(threat_list, threat_name)
		}
	}()

	size := len(original_file)

	/* Scan original file! */
	output, err := Scan(file, scanPath)
	if err != nil {
		return err
	}

	utils.PrintNewLine()

	if IsMalicious(output) {
		/* We found something! */
		utils.PrintErr("Threat detected in the original file, beginning binary search...")
		threat_names <- GetSignature(output)
	} else {
		/* found nothing, time to die */
		utils.PrintErr("No threat detected in the original file, dying now")
		return nil
	}

	tempDir := filepath.Join(".", "kaspersky")

	os.MkdirAll(tempDir, 0o755)
	testFilePath := filepath.Join(tempDir, "testfile.exe")

	lastGood := 0                    // lower range
	upperBound := len(original_file) // upper range
	mid := upperBound / 2            // pivot point

	threatFound := false
	tf_lower := 0

	for upperBound-lastGood > 1 {
		err := os.WriteFile(testFilePath, original_file[tf_lower:mid], 0o644)
		if err != nil {
			return err
		}

		utils.PrintDebug(fmt.Sprintf("Scanning from %d to %d bytes", tf_lower, mid), debug)

		output, err := Scan(testFilePath, scanPath)
		if err != nil {
			return err
		}

		if IsMalicious(output) {
			progressUpdates <- Progress{Low: tf_lower, High: mid, Malicious: true}
			utils.PrintDebug(fmt.Sprintf("Threat detected in the range %d to %d bytes", tf_lower, mid), debug)
			/* Found a threat */
			threatFound = true
			upperBound = mid
		} else {
			progressUpdates <- Progress{Low: tf_lower, High: mid, Malicious: false}
			utils.PrintDebug(fmt.Sprintf("No threat detected in the range %d to %d bytes", tf_lower, mid), debug)
			/* No threat found */
			lastGood = mid
		}

		mid = (lastGood + upperBound) / 2
	}

	os.RemoveAll(tempDir)
	end := time.Since(start)

	if threatFound {

		utils.PrintNewLine()
		utils.PrintOk(fmt.Sprintf("Kaspersky - %s", end))
		utils.PrintErr(fmt.Sprintf("Isolated bad bytes at offset 0x%X in the file [approximately %d / %d bytes]", lastGood, lastGood, size))

		start := lastGood - 32
		if start < 0 {
			start = 0
		}

		end := mid + 32
		if end > size {
			end = size
		}

		threatData := original_file[start:end]
		dump := hex.Dump(threatData)
		fmt.Println(dump)

		uniqueThreats := make(map[string]bool)
		for _, threat := range threat_list {
			uniqueThreats[threat] = true
		}

		for threat := range uniqueThreats {
			utils.PrintErr(threat)
		}

	} else {
		utils.PrintInfo("Not malicious")
	}

	ticker.Stop()
	close(progressUpdates)
	close(threat_names)

	return nil
}

func FindKaspersky() (string, error) {
	var avp string
	for _, path := range []string{ScanPath, AltScanPath} {
		if CheckIfExists(path) {
			avp = path
			break
		}
	}

	return avp, nil
}