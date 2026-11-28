package main

import "github.com/compico/overlaycomposer/pkg/ocplugin"

func ptr(v float64) *float64 { return &v }

// widgetSettings — настройки источника «Twitch chat» на сцене. Умолчания должны совпадать с ui/main.js.
func widgetSettings() []ocplugin.WidgetSetting {
	const (
		gMessages = "Messages"
		gText     = "Message text"
		gNick     = "Nickname"
		gBg       = "Background"
	)
	return []ocplugin.WidgetSetting{
		{Key: "maxMessages", Label: "Max messages", Type: "number", Group: gMessages, Default: 20, Min: ptr(1), Max: ptr(200)},
		{Key: "fadeSeconds", Label: "Hide after, seconds (0 = never)", Type: "number", Group: gMessages, Default: 0, Min: ptr(0), Max: ptr(3600)},
		{Key: "direction", Label: "New messages", Type: "select", Group: gMessages, Default: "bottom", Options: []ocplugin.SettingOption{
			{Value: "bottom", Label: "appear at the bottom"}, {Value: "top", Label: "appear at the top"}}},
		{Key: "gap", Label: "Gap between messages, px", Type: "number", Group: gMessages, Default: 6, Min: ptr(0), Max: ptr(100)},
		{Key: "hideCommands", Label: "Hide commands (!…)", Type: "bool", Group: gMessages, Default: true},
		{Key: "ignoreUsers", Label: "Ignore users", Type: "string", Group: gMessages, Default: "nightbot,streamelements,moobot",
			Help: "Через запятую, регистр не важен"},
		{Key: "showEmotes", Label: "Show Twitch emotes", Type: "bool", Group: gMessages, Default: true},

		{Key: "msgFont", Label: "Font", Type: "font", Group: gText, Default: "Segoe UI"},
		{Key: "msgSize", Label: "Size, px", Type: "number", Group: gText, Default: 28, Min: ptr(8), Max: ptr(200)},
		{Key: "msgColor", Label: "Color", Type: "color", Group: gText, Default: "#ffffff"},
		{Key: "msgBold", Label: "Bold", Type: "bool", Group: gText, Default: false},
		{Key: "shadow", Label: "Text shadow", Type: "bool", Group: gText, Default: true},

		{Key: "nickFont", Label: "Font", Type: "font", Group: gNick, Default: "Segoe UI"},
		{Key: "nickSize", Label: "Size, px", Type: "number", Group: gNick, Default: 28, Min: ptr(8), Max: ptr(200)},
		{Key: "nickBold", Label: "Bold", Type: "bool", Group: gNick, Default: true},
		{Key: "useTwitchColor", Label: "Use the user's Twitch color", Type: "bool", Group: gNick, Default: true},
		{Key: "nickColor", Label: "Color (if not Twitch color)", Type: "color", Group: gNick, Default: "#9146ff"},
		{Key: "separator", Label: "Separator after nickname", Type: "string", Group: gNick, Default: ": "},

		{Key: "bgColor", Label: "Message background", Type: "color", Group: gBg, Default: "#000000"},
		{Key: "bgOpacity", Label: "Background opacity, %", Type: "range", Group: gBg, Default: 0, Min: ptr(0), Max: ptr(100), Step: ptr(1)},
		{Key: "padding", Label: "Padding, px", Type: "number", Group: gBg, Default: 6, Min: ptr(0), Max: ptr(60)},
		{Key: "radius", Label: "Corner radius, px", Type: "number", Group: gBg, Default: 8, Min: ptr(0), Max: ptr(60)},

		{Key: "googleFonts", Label: "Load fonts from Google Fonts by name", Type: "bool", Group: "Advanced", Default: false,
			Help: "Нужен интернет; системные шрифты работают и без этого"},
	}
}
