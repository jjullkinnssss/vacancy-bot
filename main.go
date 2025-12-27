package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"google.golang.org/api/option"
	"google.golang.org/api/sheets/v4"
)

const (
	BaseURL       = "https://gsz.gov.by"
	SearchURL     = "https://gsz.gov.by/registration/vacancy-search/?profession=техник-программист&paginate_by=10"
	SpreadsheetID = "1QOd0QpM-QKhRuQn1pvSgbmzxGcRUKbN47gU5u_oT2Xg"
	CredsFile     = "credentials.json"
	MaxItems      = 5
)

type Vacancy struct {
	Title        string
	Salary       string
	Company      string
	Address      string
	Education    string
	ContactName  string
	ContactPhone string
	URL          string
}

var sheetsService *sheets.Service

// URL -> row
var urlToRow = map[string]int{}

// row -> taken (column J)
var taken = map[int]string{}

// подписанные чаты
var chats = map[int64]bool{}

// chatID -> ФИО
var users = map[int64]string{
	1414802865: "Подтероб Юлия Сергеевна",
	741457312:  "Сасим Ярослав Сергеевич",
}

// ===================== MAIN =====================

func main() {
	botToken := os.Getenv("BOT_TOKEN")
	if botToken == "" {
		log.Fatal("❌ BOT_TOKEN is not set")
	}

	initSheets()
	loadExisting()

	bot, err := tgbotapi.NewBotAPI(botToken)
	if err != nil {
		log.Fatal(err)
	}

	log.Println("🤖 Бот запущен")

	go dailyScheduler(bot)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	for upd := range bot.GetUpdatesChan(u) {

		if upd.Message != nil {
			chatID := upd.Message.Chat.ID
			chats[chatID] = true
			go sendVacanciesToChat(bot, chatID)
		}

		if upd.CallbackQuery != nil {
			chats[upd.CallbackQuery.Message.Chat.ID] = true
			handleCallback(bot, upd.CallbackQuery)
		}
	}
}

// ===================== SCHEDULER =====================

func dailyScheduler(bot *tgbotapi.BotAPI) {
	for {
		now := time.Now()
		next := time.Date(
			now.Year(),
			now.Month(),
			now.Day(),
			17, 0, 0, 0,
			now.Location(),
		)

		if now.After(next) {
			next = next.Add(24 * time.Hour)
		}

		time.Sleep(time.Until(next))

		log.Println("⏰ 17:00 — ежедневная рассылка")
		for chatID := range chats {
			sendVacanciesToChat(bot, chatID)
		}
	}
}

// ===================== GOOGLE SHEETS =====================

func initSheets() {
	ctx := context.Background()
	var err error

	sheetsService, err = sheets.NewService(
		ctx,
		option.WithCredentialsFile(CredsFile),
		option.WithScopes(sheets.SpreadsheetsScope),
	)
	if err != nil {
		log.Fatal(err)
	}
}

func loadExisting() {
	resp, err := sheetsService.Spreadsheets.Values.Get(
		SpreadsheetID,
		"I2:J",
	).Do()
	if err != nil {
		log.Fatal(err)
	}

	for i, row := range resp.Values {
		r := i + 2
		if len(row) > 0 {
			urlToRow[row[0].(string)] = r
		}
		if len(row) > 1 && row[1] != "" {
			taken[r] = row[1].(string)
		}
	}
}

func appendVacancy(v Vacancy) int {
	values := [][]interface{}{{
		time.Now().Format("02.01.2006"),
		v.Title,
		v.Salary,
		v.Company,
		v.Address,
		v.Education,
		v.ContactName,
		v.ContactPhone,
		v.URL,
		"",
	}}

	resp, err := sheetsService.Spreadsheets.Values.Append(
		SpreadsheetID,
		"A2",
		&sheets.ValueRange{Values: values},
	).ValueInputOption("RAW").Do()

	if err != nil {
		return 0
	}

	rowStr := strings.TrimLeft(strings.Split(resp.Updates.UpdatedRange, "!")[1], "A")
	row, _ := strconv.Atoi(rowStr)
	urlToRow[v.URL] = row
	return row
}

func markTaken(row int, name string) {
	sheetsService.Spreadsheets.Values.Update(
		SpreadsheetID,
		fmt.Sprintf("J%d", row),
		&sheets.ValueRange{Values: [][]interface{}{{name}}},
	).ValueInputOption("RAW").Do()

	taken[row] = name
}

// ===================== TELEGRAM =====================

func handleCallback(bot *tgbotapi.BotAPI, q *tgbotapi.CallbackQuery) {
	bot.Request(tgbotapi.NewCallback(q.ID, ""))

	parts := strings.Split(q.Data, "|")
	if parts[0] != "apply" {
		return
	}

	row, _ := strconv.Atoi(parts[1])
	if _, ok := taken[row]; ok {
		return
	}

	name := users[q.From.ID]
	if name == "" {
		name = fmt.Sprintf("chat:%d", q.From.ID)
	}

	markTaken(row, name)

	edit := tgbotapi.NewEditMessageReplyMarkup(
		q.Message.Chat.ID,
		q.Message.MessageID,
		keyboard(row),
	)
	bot.Send(edit)
}

func keyboard(row int) tgbotapi.InlineKeyboardMarkup {
	if _, ok := taken[row]; ok {
		return tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🕓 На рассмотрении", "noop"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonURL(
					"📊 Таблица",
					"https://docs.google.com/spreadsheets/d/"+SpreadsheetID,
				),
			),
		)
	}

	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(
				"✅ Откликнуться",
				fmt.Sprintf("apply|%d", row),
			),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonURL(
				"📊 Таблица",
				"https://docs.google.com/spreadsheets/d/"+SpreadsheetID,
			),
		),
	)
}

// ===================== SENDING =====================

func sendVacanciesToChat(bot *tgbotapi.BotAPI, chatID int64) {
	for _, v := range fetchVacancies() {

		row, ok := urlToRow[v.URL]
		if !ok {
			row = appendVacancy(v)
		}

		msg := tgbotapi.NewMessage(chatID, formatVacancy(v))
		msg.ReplyMarkup = keyboard(row)
		bot.Send(msg)
	}
}

// ===================== PARSER =====================

func fetchVacancies() []Vacancy {
	resp, err := http.Get(SearchURL)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	doc, _ := goquery.NewDocumentFromReader(resp.Body)
	var list []Vacancy

	doc.Find("h4.job-title a").EachWithBreak(func(i int, s *goquery.Selection) bool {
		href, _ := s.Attr("href")
		v, err := parseVacancy(BaseURL + href)
		if err == nil {
			v.Title = strings.TrimSpace(s.Text())
			list = append(list, *v)
		}
		return len(list) < MaxItems
	})

	return list
}

func parseVacancy(url string) (*Vacancy, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	doc, _ := goquery.NewDocumentFromReader(resp.Body)

	get := func(label string) string {
		return strings.TrimSpace(
			doc.Find("p:contains('" + label + "')").Parent().Next().Text(),
		)
	}

	return &Vacancy{
		URL:          url,
		Salary:       get("Заработная плата"),
		Address:      get("Адрес рабочего места"),
		Education:    get("Образование"),
		Company:      strings.TrimSpace(doc.Find("#vacancy-detail h5").First().Text()),
		ContactName:  get("ФИО"),
		ContactPhone: strings.TrimSpace(doc.Find("a[href^='tel:']").First().Text()),
	}, nil
}

// ===================== FORMAT =====================

func formatVacancy(v Vacancy) string {
	return fmt.Sprintf(
		"📌 %s\n💰 %s\n🏢 %s\n📍 %s\n🎓 %s\n👤 %s\n📞 %s\n🔗 %s",
		v.Title,
		empty(v.Salary),
		v.Company,
		v.Address,
		v.Education,
		v.ContactName,
		v.ContactPhone,
		v.URL,
	)
}

func empty(s string) string {
	if strings.TrimSpace(s) == "" {
		return "не указано"
	}
	return s
}
