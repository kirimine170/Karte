package marp

import (
	"fmt"
	"html"
	"regexp"
	"strings"
)

// ParseSlides parses Marp markdown content into individual slides
// Slides are separated by `---` (horizontal rule)
func ParseSlides(content string) []string {
	// Remove leading/trailing whitespace
	content = strings.TrimSpace(content)
	if content == "" {
		return []string{}
	}

	// Split by horizontal rule (---) that appears on its own line
	// This regex matches `---` surrounded by newlines or at start/end
	slideRegex := regexp.MustCompile(`(?m)^---\s*$`)

	// Split content by slide separators
	parts := slideRegex.Split(content, -1)

	var slides []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			slides = append(slides, part)
		}
	}

	// If no slides were found (no separators), treat entire content as one slide
	if len(slides) == 0 {
		slides = []string{content}
	}

	return slides
}

// RenderSlideHTML converts a single slide markdown content to HTML
// This is a simplified markdown to HTML converter for slides
func RenderSlideHTML(slideContent string) string {
	lines := strings.Split(slideContent, "\n")
	var htmlLines []string

	for _, line := range lines {
		line = strings.TrimRight(line, " \t")

		// Headers
		if strings.HasPrefix(line, "# ") {
			text := strings.TrimPrefix(line, "# ")
			htmlLines = append(htmlLines, fmt.Sprintf("<h1>%s</h1>", html.EscapeString(text)))
		} else if strings.HasPrefix(line, "## ") {
			text := strings.TrimPrefix(line, "## ")
			htmlLines = append(htmlLines, fmt.Sprintf("<h2>%s</h2>", html.EscapeString(text)))
		} else if strings.HasPrefix(line, "### ") {
			text := strings.TrimPrefix(line, "### ")
			htmlLines = append(htmlLines, fmt.Sprintf("<h3>%s</h3>", html.EscapeString(text)))
		} else if strings.HasPrefix(line, "#### ") {
			text := strings.TrimPrefix(line, "#### ")
			htmlLines = append(htmlLines, fmt.Sprintf("<h4>%s</h4>", html.EscapeString(text)))
		} else if line == "" {
			htmlLines = append(htmlLines, "<br>")
		} else {
			// Process inline formatting
			processed := processInlineFormatting(line)
			htmlLines = append(htmlLines, fmt.Sprintf("<p>%s</p>", processed))
		}
	}

	return strings.Join(htmlLines, "\n")
}

// processInlineFormatting processes markdown inline formatting
func processInlineFormatting(text string) string {
	// Bold: **text**
	text = regexp.MustCompile(`\*\*(.*?)\*\*`).ReplaceAllStringFunc(text, func(match string) string {
		content := regexp.MustCompile(`\*\*(.*?)\*\*`).FindStringSubmatch(match)[1]
		return fmt.Sprintf("<strong>%s</strong>", html.EscapeString(content))
	})

	// Italic: *text*
	text = regexp.MustCompile(`\*(.*?)\*`).ReplaceAllStringFunc(text, func(match string) string {
		content := regexp.MustCompile(`\*(.*?)\*`).FindStringSubmatch(match)[1]
		// Avoid matching bold markers
		if !strings.HasPrefix(match, "**") && !strings.HasSuffix(match, "**") {
			return fmt.Sprintf("<em>%s</em>", html.EscapeString(content))
		}
		return match
	})

	// Code: `text`
	text = regexp.MustCompile("`([^`]+)`").ReplaceAllStringFunc(text, func(match string) string {
		content := regexp.MustCompile("`([^`]+)`").FindStringSubmatch(match)[1]
		return fmt.Sprintf("<code>%s</code>", html.EscapeString(content))
	})

	// Escape remaining HTML
	return html.EscapeString(text)
}

// RenderMarpHTML generates complete HTML for Marp presentation
func RenderMarpHTML(slides []string, title string) string {
	var slideHTMLs []string

	for i, slide := range slides {
		slideHTML := RenderSlideHTML(slide)
		slideHTMLs = append(slideHTMLs, fmt.Sprintf(`
			<section class="slide" data-slide-index="%d">
				<div class="slide-content">
					%s
				</div>
			</section>`, i, slideHTML))
	}

	html := fmt.Sprintf(`<!DOCTYPE html>
<html lang="ja">
<head>
	<meta charset="UTF-8">
	<meta name="viewport" content="width=device-width, initial-scale=1.0">
	<title>%s</title>
	<style>
		* {
			margin: 0;
			padding: 0;
			box-sizing: border-box;
		}
		
		body {
			font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif;
			background: #1a1a1a;
			color: #ffffff;
			overflow: hidden;
		}
		
		#presentation {
			width: 100vw;
			height: 100vh;
			display: flex;
			align-items: center;
			justify-content: center;
			position: relative;
		}
		
		.slide {
			display: none;
			width: 100%%;
			height: 100%%;
			padding: 80px;
			background: #1a1a1a;
			overflow-y: auto;
		}
		
		.slide.active {
			display: flex;
			align-items: center;
			justify-content: center;
			flex-direction: column;
		}
		
		.slide-content {
			max-width: 1200px;
			width: 100%%;
			text-align: center;
		}
		
		.slide h1 {
			font-size: 3.5em;
			margin-bottom: 0.5em;
			color: #ffffff;
		}
		
		.slide h2 {
			font-size: 2.5em;
			margin-bottom: 0.5em;
			color: #ffffff;
		}
		
		.slide h3 {
			font-size: 2em;
			margin-bottom: 0.5em;
			color: #ffffff;
		}
		
		.slide h4 {
			font-size: 1.5em;
			margin-bottom: 0.5em;
			color: #ffffff;
		}
		
		.slide p {
			font-size: 1.5em;
			line-height: 1.6;
			margin: 1em 0;
			color: #e0e0e0;
		}
		
		.slide code {
			background: #2d2d2d;
			padding: 0.2em 0.4em;
			border-radius: 3px;
			font-family: "Monaco", "Courier New", monospace;
			font-size: 0.9em;
		}
		
		.slide strong {
			color: #ffffff;
			font-weight: 600;
		}
		
		.slide em {
			font-style: italic;
		}
		
		.navigation {
			position: fixed;
			bottom: 20px;
			left: 50%%;
			transform: translateX(-50%%);
			display: flex;
			gap: 10px;
			z-index: 1000;
		}
		
		.nav-button {
			background: rgba(255, 255, 255, 0.1);
			border: 1px solid rgba(255, 255, 255, 0.2);
			color: #ffffff;
			padding: 10px 20px;
			border-radius: 5px;
			cursor: pointer;
			font-size: 14px;
			transition: background 0.2s;
		}
		
		.nav-button:hover {
			background: rgba(255, 255, 255, 0.2);
		}
		
		.nav-button:disabled {
			opacity: 0.5;
			cursor: not-allowed;
		}
		
		.slide-counter {
			position: fixed;
			top: 20px;
			right: 20px;
			color: rgba(255, 255, 255, 0.6);
			font-size: 14px;
			z-index: 1000;
		}
		
		/* Keyboard navigation hint */
		.keyboard-hint {
			position: fixed;
			bottom: 80px;
			left: 50%%;
			transform: translateX(-50%%);
			color: rgba(255, 255, 255, 0.4);
			font-size: 12px;
			z-index: 1000;
		}
	</style>
</head>
<body>
	<div id="presentation">
		%s
	</div>
	
	<div class="slide-counter">
		<span id="current-slide">1</span> / <span id="total-slides">%d</span>
	</div>
	
	<div class="navigation">
		<button class="nav-button" id="prev-btn" onclick="previousSlide()">← 前へ</button>
		<button class="nav-button" id="next-btn" onclick="nextSlide()">次へ →</button>
	</div>
	
	<div class="keyboard-hint">
		キーボード: ← → キーでスライドを切り替え
	</div>
	
	<script>
		let currentSlide = 0;
		const slides = document.querySelectorAll('.slide');
		const totalSlides = slides.length;
		
		document.getElementById('total-slides').textContent = totalSlides;
		
		function showSlide(index) {
			if (index < 0 || index >= totalSlides) return;
			
			slides.forEach(slide => slide.classList.remove('active'));
			slides[index].classList.add('active');
			currentSlide = index;
			
			document.getElementById('current-slide').textContent = index + 1;
			
			// Update navigation buttons
			document.getElementById('prev-btn').disabled = (index === 0);
			document.getElementById('next-btn').disabled = (index === totalSlides - 1);
		}
		
		function nextSlide() {
			if (currentSlide < totalSlides - 1) {
				showSlide(currentSlide + 1);
			}
		}
		
		function previousSlide() {
			if (currentSlide > 0) {
				showSlide(currentSlide - 1);
			}
		}
		
		// Keyboard navigation
		document.addEventListener('keydown', (e) => {
			if (e.key === 'ArrowRight' || e.key === ' ') {
				e.preventDefault();
				nextSlide();
			} else if (e.key === 'ArrowLeft') {
				e.preventDefault();
				previousSlide();
			}
		});
		
		// Initialize first slide
		showSlide(0);
	</script>
</body>
</html>`, html.EscapeString(title), strings.Join(slideHTMLs, "\n"), len(slides))

	return html
}
