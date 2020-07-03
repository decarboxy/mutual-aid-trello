/*
Copyright © 2020 Sam DeLuca (sam@decarboxy.com)

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package cmd

import (
	"encoding/binary"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/spf13/cobra"
	"io/ioutil"
	"log"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	completedList = "5e7d45a393cb705078c08e5b"
)

var (
	ApiKey     string
	Token      string
	OutputPath string
)

type AmountPaidField struct {
	Id            string            `json:"id"`
	Value         map[string]string `json:"value"`
	CustomfieldId string            `json:"idCustomField"`
}

func (f *AmountPaidField) GetAmountPaid() (amount int, err error) {
	amount, err = strconv.Atoi(f.Value["number"])
	return
}

type CardAction struct {
	Id   string         `json:"id"`
	Data CardActionData `json:"data"`
	Date string         `json:"date"`
	Type string         `json:"type"`
}

type CardActionData struct {
	ListBefore CardActionItem `json:"listBefore"`
	ListAfter  CardActionItem `json:"listAfter"`
}

type CardActionItem struct {
	Id   string `json:"id"`
	Name string `json:"name"`
}

type TrelloCard struct {
	Title            string `json:"name"`
	Id               string `json:"id"`
	Description      string `json:"desc"`
	Reason           string
	Name             string
	Email            string
	Institution      string
	Location         string
	AmountPaid       int
	FundTransferDate string
	RequestDate      string
}

func (*TrelloCard) CsvHeader() []string {
	return []string{
		"Name",
		"Email",
		"Institution",
		"Location",
		"Amount Paid",
		"Reason",
		"Fund Transfer Date",
		"Request Date"}
}

func (c *TrelloCard) CsvRow() []string {
	return []string{
		c.Name,
		c.Email,
		c.Institution,
		c.Location,
		strconv.Itoa(c.AmountPaid),
		c.Reason,
		c.FundTransferDate,
		c.RequestDate,
	}
}

func getAndBackoff(url string) (resp *http.Response, err error) {
	retryLimit := 10
	retryCount := 0
	for true {
		if retryCount >= retryLimit {
			err = errors.New("retry limit exceeded")
			return
		}
		resp, err = http.Get(url)
		if err != nil {
			return
		}

		if resp.StatusCode != 429 {
			return
		} else {
			retryCount += 1
			retryTime := int64(math.Pow(2, float64(retryCount)))
			fmt.Printf("Being ratelimited, waiting %d and trying again\n", retryTime)
			time.Sleep(time.Duration(retryTime) * time.Second)
			continue
		}
	}
	return
}

func (c *TrelloCard) inflateAmountPaid(apiKey string, token string) (err error) {
	getCustomFields := fmt.Sprintf("https://api.trello.com/1/cards/%s/customFieldItems?key=%s&token=%s", c.Id, apiKey, token)

	resp, err := getAndBackoff(getCustomFields)
	if err != nil {
		return
	}

	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return
	}

	var amountPaid []AmountPaidField
	err = json.Unmarshal(body, &amountPaid)
	if err != nil {
		return
	}

	if len(amountPaid) == 0 {
		err = errors.New(fmt.Sprintf("%s is missing an amount paid value", c.Title))
		return
	}

	//We only have 1 custom field
	c.AmountPaid, err = amountPaid[0].GetAmountPaid()
	return
}

func (c *TrelloCard) inflateCardHistory(apiKey string, token string) (err error) {
	getActions := fmt.Sprintf("https://api.trello.com/1/cards/%s/actions?key=%s&token=%s&.filter=updateCard:idList", c.Id, apiKey, token)

	resp, err := getAndBackoff(getActions)
	if err != nil {
		return
	}

	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return
	}

	var cardActions []CardAction
	err = json.Unmarshal(body, &cardActions)
	if err != nil {
		return
	}

	for _, action := range cardActions {
		if action.Type == "updateCard" && action.Data.ListAfter.Id == completedList {
			var parsedTime time.Time
			parsedTime, err = time.Parse(time.RFC3339, action.Date)
			if err != nil {
				return
			}
			c.FundTransferDate = parsedTime.Format("02 Jan 06 15:04")
		}
	}
	return
}

func (c *TrelloCard) inflateRequestDate() (err error) {
	// This is wild: https://help.trello.com/article/759-getting-the-time-a-card-or-board-was-created

	hexDate := c.Id[0:8]
	timeBytes, err := hex.DecodeString(hexDate)
	if err != nil {
		return
	}

	// haha trello is going to have problems with the 2038 bug
	timestamp := binary.BigEndian.Uint32(timeBytes)
	c.RequestDate = time.Unix(int64(timestamp), 0).Format("02 Jan 06 15:04")
	return
}

func (c *TrelloCard) Inflate(apiKey string, token string) (err error) {
	descriptionLines := strings.Split(c.Description, "\n")
	for _, line := range descriptionLines {
		fields := strings.Split(line, ":")
		switch fields[0] {
		case "Name":
			c.Name = strings.TrimSpace(fields[1])
		case "Email":
			c.Email = strings.TrimSpace(fields[1])
		case "Institution":
			c.Institution = strings.TrimSpace(fields[1])
		case "Location":
			c.Location = strings.TrimSpace(fields[1])
		case "Description":
			c.Reason = strings.TrimSpace(fields[1])
		}
	}

	err = c.inflateAmountPaid(apiKey, token)
	if err != nil {
		return
	}

	err = c.inflateCardHistory(apiKey, token)
	if err != nil {
		return
	}

	err = c.inflateRequestDate()
	return
}

// csvCmd represents the csv command
var csvCmd = &cobra.Command{
	Use:   "csv",
	Short: "export requests in CSV form",
	Run: func(cmd *cobra.Command, args []string) {

		getCompletedCards := fmt.Sprintf("https://api.trello.com/1/lists/%s/cards?key=%s&token=%s", completedList, ApiKey, Token)

		resp, err := getAndBackoff(getCompletedCards)
		if err != nil {
			log.Fatal(err)
		}

		defer resp.Body.Close()

		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			log.Fatal(err)
		}

		var cards []TrelloCard
		err = json.Unmarshal(body, &cards)
		if err != nil {
			log.Fatal(err)
		}

		outCsv, err := os.Create("output.csv")
		if err != nil {
			log.Fatal(err)
		}
		defer outCsv.Close()

		csvWriter := csv.NewWriter(outCsv)
		err = csvWriter.Write(cards[0].CsvHeader())
		if err != nil {
			log.Fatal(err)
		}

		for _, card := range cards {
			err = card.Inflate(ApiKey, Token)
			if err != nil {
				log.Fatal(err)
			}
			err = csvWriter.Write(card.CsvRow())
			if err != nil {
				log.Fatal(err)
			}

		}

	},
}

func init() {
	rootCmd.AddCommand(csvCmd)

	csvCmd.Flags().StringVar(&ApiKey, "api-key", "", "A trello API key")
	csvCmd.Flags().StringVar(&Token, "token", "", "A trello Token")
	csvCmd.Flags().StringVar(&OutputPath, "out", "recipients.csv", "the path to an output csv file")
	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// csvCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// csvCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
