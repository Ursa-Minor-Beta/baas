package dto

type HtmlTemplate struct {
	PrependHtml *string `json:"prependHtml,omitempty" yaml:"prependHtml,omitempty"`
	AppendHtml  *string `json:"appendHtml,omitempty" yaml:"appendHtml,omitempty"`

	HeaderLogo         *string  `json:"headerLogo,omitempty" yaml:"headerLogo,omitempty"`
	HeaderLogoWidth    *float64 `json:"headerLogoWidth,omitempty" yaml:"headerLogoWidth,omitempty"`
	HeaderLogoHeight   *float64 `json:"headerLogoHeight,omitempty" yaml:"headerLogoHeight,omitempty"`
	HeaderLogoPosition *string  `json:"headerLogoPosition,omitempty" yaml:"headerLogoPosition,omitempty"`
	HeaderText         *string  `json:"headerText,omitempty" yaml:"headerText,omitempty"`
	HeaderHeight       *float64 `json:"headerHeight,omitempty" yaml:"headerHeight,omitempty"`
	FooterText         *string  `json:"footerText,omitempty" yaml:"footerText,omitempty"`
	ShowPageNumbers    *bool    `json:"showPageNumbers,omitempty" yaml:"showPageNumbers,omitempty"`

	PrimaryColor   *string `json:"primaryColor,omitempty" yaml:"primaryColor,omitempty"`
	SecondaryColor *string `json:"secondaryColor,omitempty" yaml:"secondaryColor,omitempty"`
	AccentColor    *string `json:"accentColor,omitempty" yaml:"accentColor,omitempty"`
	TextColor      *string `json:"textColor,omitempty" yaml:"textColor,omitempty"`

	FontFamily   *string  `json:"fontFamily,omitempty" yaml:"fontFamily,omitempty"`
	HeadingFont  *string  `json:"headingFont,omitempty" yaml:"headingFont,omitempty"`
	FontSize     *float64 `json:"fontSize,omitempty" yaml:"fontSize,omitempty"`
	HeadingScale *float64 `json:"headingScale,omitempty" yaml:"headingScale,omitempty"`

	Margins *HtmlTemplateMargins `json:"margins,omitempty" yaml:"margins,omitempty"`

	CompanyName    *string `json:"companyName,omitempty" yaml:"companyName,omitempty"`
	CompanyTagline *string `json:"companyTagline,omitempty" yaml:"companyTagline,omitempty"`
	DocumentTitle  *string `json:"documentTitle,omitempty" yaml:"documentTitle,omitempty"`

	CustomCss *string `json:"customCss,omitempty" yaml:"customCss,omitempty"`
}

type HtmlTemplateMargins struct {
	Top    *float32 `json:"top,omitempty" yaml:"top,omitempty"`
	Right  *float32 `json:"right,omitempty" yaml:"right,omitempty"`
	Bottom *float32 `json:"bottom,omitempty" yaml:"bottom,omitempty"`
	Left   *float32 `json:"left,omitempty" yaml:"left,omitempty"`
}
