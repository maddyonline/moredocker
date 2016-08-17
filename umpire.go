package umpire

import (
	"bufio"
	"bytes"
	"encoding/json"
	"github.com/docker/engine-api/client"
	"golang.org/x/net/context"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	_ "time"
)

type TestCase struct {
	Input    io.Reader
	Expected io.Reader
	Id       string
}

type Umpire struct {
	Client      *client.Client
	ProblemsDir string
}

type Decision string

const (
	Fail Decision = "fail"
	Pass          = "pass"
)

type Response struct {
	Status  Decision `json:"status"`
	Details string   `json:"details"`
	Stdout  string   `json:"stdout", omitempty`
	Stderr  string   `json:"stderr", omitempty`
}

func createDirectoryWithFiles(files []*InMemoryFile) (*string, error) {
	dir, err := ioutil.TempDir(".", "work_dir_")
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		tmpfn := filepath.Join(dir, file.Name)
		if err := ioutil.WriteFile(tmpfn, []byte(file.Content), 0666); err != nil {
			return nil, err
		}
		log.Println(tmpfn)
	}
	return &dir, nil
}

func loadTestCases(problemsDir string, payload *Payload) ([]*TestCase, error) {
	testcases := []*TestCase{}
	files, err := ioutil.ReadDir(filepath.Join(problemsDir, payload.Problem.Id, "testcases"))
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		if strings.Contains(file.Name(), "input") {
			inputFilename := file.Name()
			expectedFilename := strings.Replace(file.Name(), "input", "output", 1)
			input, err := os.Open(filepath.Join(problemsDir, payload.Problem.Id, "testcases", inputFilename))
			if err != nil {
				return nil, err
			}
			expected, err := os.Open(filepath.Join(problemsDir, payload.Problem.Id, "testcases", expectedFilename))
			if err != nil {
				return nil, err
			}
			testcases = append(testcases, &TestCase{input, expected, inputFilename})
		}
	}
	return testcases, nil
}

func (u *Umpire) JudgeTestcase(ctx context.Context, payload *Payload, stdout, stderr io.Writer, testcase *TestCase) error {
	workDir, err := createDirectoryWithFiles(payload.Files)
	if err != nil {
		return err
	}
	defer func() {
		log.Printf("Removing temporary working directory: %s", *workDir)
		os.RemoveAll(*workDir)
	}()
	testcaseData, err := ioutil.ReadAll(testcase.Input)
	if err != nil {
		return err
	}
	payloadToSend := &Payload{}
	*payloadToSend = *payload
	payloadToSend.Stdin = string(testcaseData)
	return DockerJudge(ctx, u.Client, payloadToSend, stdout, stderr, bufio.NewScanner(testcase.Expected))
}

func (u *Umpire) JudgeAll(ctx context.Context, payload *Payload, stdout, stderr io.Writer) error {
	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(ctx)
	errors := make(chan error)
	testcases, err := loadTestCases(u.ProblemsDir, payload)
	if err != nil {
		return err
	}
	for i, testcase := range testcases {
		wg.Add(1)
		go func(ctx context.Context, i int, testcase *TestCase) {
			defer wg.Done()
			err := u.JudgeTestcase(ctx, payload, ioutil.Discard, ioutil.Discard, testcase)
			log.Printf("testcase %d: %v", i, err)
			if err != nil {
				cancel()
			}
			errors <- err
		}(ctx, i, testcase)
	}
	go func() {
		wg.Wait()
		close(errors)
	}()
	var finalErr error
	var fail int
	for err := range errors {
		if err != nil {
			fail += 1
			if !strings.Contains(err.Error(), "Context cancelled") {
				finalErr = err
			}
		}
		// if err != nil && strings.Contains(err.Error(), "Mismatch") {
		// 	cancel()
		// }
	}
	log.Printf("Fail: %d", fail)
	return finalErr
}

func (u *Umpire) RunAndJudge(ctx context.Context, payload *Payload, stdout, stderr io.Writer) error {
	r, w := io.Pipe()
	correctlySolve := func(w io.Writer) {
		data, err := ioutil.ReadFile(filepath.Join(u.ProblemsDir, payload.Problem.Id, "solution.json"))
		if err != nil {
			return
		}
		payloadToSend := &Payload{}
		err = json.Unmarshal(data, payloadToSend)
		if err != nil {
			return
		}
		payloadToSend.Stdin = payload.Stdin
		DockerRun(ctx, u.Client, payloadToSend, w, ioutil.Discard)
	}

	go correctlySolve(w)

	testcase := &TestCase{
		Input:    strings.NewReader(payload.Stdin),
		Expected: r,
	}
	return u.JudgeTestcase(context.Background(), payload, stdout, stderr, testcase)
}

func JudgeDefault(u *Umpire, payload *Payload) *Response {
	err := u.JudgeAll(context.Background(), payload, ioutil.Discard, ioutil.Discard)
	if err != nil {
		return &Response{
			Status:  Fail,
			Details: err.Error(),
		}
	}
	return &Response{
		Status: Pass,
	}
}

func RunDefault(u *Umpire, payload *Payload) *Response {
	var stdout, stderr bytes.Buffer
	err := u.RunAndJudge(context.Background(), payload, &stdout, &stderr)
	if err != nil {
		return &Response{Fail, err.Error(), stdout.String(), stderr.String()}
	}
	return &Response{Pass, "Output is as expected", stdout.String(), stderr.String()}
}
