// jsCrawler.go
// A sequential web crawler in Go that:
// 1. Accepts a target domain (and optional HTTP scheme) as command-line arguments
// 2. Recursively crawls all pages under the same domain
// 3. Extracts every JavaScript file and modulepreload/prefetch URLs ending with .js
// 4. Writes discovered JS URLs to "<domain>_all_js.txt"
// 5. Tests each JS URL for HTTP status:
//    - Status < 400: written to "<domain>_good_js.txt"
//    - Status >= 400 or network error: written to "<domain>_bad_js.txt"

package main

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"golang.org/x/net/html"
)

func main() {
	// Parse arguments
	if len(os.Args) < 2 {
		fmt.Println("Usage: go run jsCrawler.go <domain> [http|https]")
		os.Exit(1)
	}
	domain := os.Args[1]
	scheme := "https"
	if len(os.Args) >= 3 {
		scheme = strings.TrimRight(os.Args[2], ":/")
	}
	root := fmt.Sprintf("%s://%s/", scheme, domain)

	fmt.Printf("[DEBUG] Starting crawl for %s\n", root)

	// Crawl
	seen := map[string]bool{root: true}
	queue := []string{root}
	jsSet := map[string]bool{}

	for len(queue) > 0 {
		page := queue[0]
		queue = queue[1:]
		fmt.Printf("[DEBUG] Crawling page: %s\n", page)

		resp, err := http.Get(page)
		if err != nil {
			fmt.Printf("[ERROR] Fetch %s: %v\n", page, err)
			continue
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			fmt.Printf("[ERROR] Read %s: %v\n", page, err)
			continue
		}
		content := string(body)

		// Extract JS URLs
		for _, js := range extractJS(content, page) {
			jsSet[js] = true
		}
		// Extract links
		for _, link := range extractLinks(content, page) {
			if sameDomain(link, domain) && !seen[link] {
				seen[link] = true
				queue = append(queue, link)
			}
		}
	}

	if len(jsSet) == 0 {
		fmt.Printf("[DEBUG] No JS files found; exiting.\n")
		return
	}

	// Prepare files
	allFile := fmt.Sprintf("%s_all_js.txt", domain)
	goodFile := fmt.Sprintf("%s_good_js.txt", domain)
	badFile := fmt.Sprintf("%s_bad_js.txt", domain)

	af, err := os.Create(allFile)
	if err != nil {
		fmt.Printf("[ERROR] Create %s: %v\n", allFile, err)
		return
	}
	defer af.Close()
	gf, err := os.Create(goodFile)
	if err != nil {
		fmt.Printf("[ERROR] Create %s: %v\n", goodFile, err)
		return
	}
	defer gf.Close()
	bf, err := os.Create(badFile)
	if err != nil {
		fmt.Printf("[ERROR] Create %s: %v\n", badFile, err)
		return
	}
	defer bf.Close()

	aw := bufio.NewWriter(af)
	gw := bufio.NewWriter(gf)
	bw := bufio.NewWriter(bf)

	// Write all
	for js := range jsSet {
		fmt.Fprintln(aw, js)
	}
	aw.Flush()
	fmt.Printf("[DEBUG] Wrote all JS to %s\n", allFile)

	// Test and classify
	fmt.Println("[DEBUG] Testing JS files...")
	for js := range jsSet {
		resp, err := http.Get(js)
		if err != nil {
			fmt.Printf("[ERROR] Fetch JS %s: %v\n", js, err)
			fmt.Fprintln(bw, js)
			continue
		}
		status := resp.StatusCode
		resp.Body.Close()
		if status >= 400 {
			fmt.Printf("[FLAG] %s returned %d\n", js, status)
			fmt.Fprintln(bw, js)
		} else {
			fmt.Printf("[OK]   %s returned %d\n", js, status)
			fmt.Fprintln(gw, js)
		}
	}
	gw.Flush()
	bw.Flush()

	fmt.Printf("[DEBUG] Good JS in %s, bad JS in %s\n", goodFile, badFile)
	fmt.Printf("[DEBUG] Pages visited: %d, JS files found: %d\n", len(seen), len(jsSet))
}

// extractJS finds <script src> and <link rel=modulepreload|prefetch as=script> URLs ending with .js
func extractJS(htmlContent, base string) []string {
	var out []string
	doc, err := html.Parse(strings.NewReader(htmlContent))
	if err != nil {
		fmt.Printf("[ERROR] Parse HTML %s: %v\n", base, err)
		return out
	}
	var rec func(*html.Node)
	rec = func(n *html.Node) {
		if n.Type == html.ElementNode {
			if n.Data == "script" {
				for _, a := range n.Attr {
					if a.Key == "src" {
						u := resolveURL(base, a.Val)
						if strings.HasSuffix(u, ".js") {
							out = append(out, u)
						}
					}
				}
			}
			if n.Data == "link" {
				var rel, as, href string
				for _, a := range n.Attr {
					switch a.Key {
					case "rel":
						rel = a.Val
					case "as":
						as = a.Val
					case "href":
						href = a.Val
					}
				}
				if (rel == "modulepreload" || rel == "prefetch") && as == "script" {
					u := resolveURL(base, href)
					if strings.HasSuffix(u, ".js") {
						out = append(out, u)
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			rec(c)
		}
	}
	rec(doc)
	return out
}

// extractLinks finds <a href> URLs
func extractLinks(htmlContent, base string) []string {
	var out []string
	doc, err := html.Parse(strings.NewReader(htmlContent))
	if err != nil {
		return out
	}
	var rec func(*html.Node)
	rec = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			for _, a := range n.Attr {
				if a.Key == "href" {
					u := resolveURL(base, a.Val)
					out = append(out, u)
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			rec(c)
		}
	}
	rec(doc)
	return out
}

// resolveURL makes href absolute against base
func resolveURL(base, href string) string {
	u, err := url.Parse(href)
	if err != nil {
		return ""
	}
	if u.IsAbs() {
		return u.String()
	}
	bu, err := url.Parse(base)
	if err != nil {
		return ""
	}
	return bu.ResolveReference(u).String()
}

// sameDomain ensures link host matches domain
func sameDomain(link, domain string) bool {
	u, err := url.Parse(link)
	return err == nil && u.Host == domain
}
// jscrwal/jscrawl.go	
