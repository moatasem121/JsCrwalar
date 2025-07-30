// jsCrawler.go
// A sequential (one-page-at-a-time) web crawler that:
// - Accepts a domain (and optional scheme) from the command line
// - Crawls all internal pages of the site
// - Extracts JavaScript URLs from HTML tags
// - Tests JS files for availability (HTTP status)
// - Saves all JS, good JS (status < 400), and bad JS (>= 400 or failed) into separate files

package main

// === Import standard libraries ===
import (
	"bufio"           // For buffered writing to files
	"fmt"             // For printing output to terminal
	"io"              // For reading HTTP response bodies
	"net/http"        // To make HTTP GET requests
	"net/url"         // For parsing and resolving URLs
	"os"              // For file operations and accessing command-line args
	"strings"         // For string manipulation

	"golang.org/x/net/html" // External package to parse raw HTML documents
)

func main() {
	// === Step 1: Parse command-line arguments ===
	if len(os.Args) < 2 {
		// If user didn't provide a domain, show usage help and exit
		fmt.Println("Usage: go run jsCrawler.go <domain> [http|https]")
		os.Exit(1)
	}

	// First argument is the domain (e.g., "example.com")
	domain := os.Args[1]

	// Default to "https" if no scheme is provided
	scheme := "https"
	if len(os.Args) >= 3 {
		// If scheme is provided, remove any trailing colons or slashes
		scheme = strings.TrimRight(os.Args[2], ":/")
	}

	// Build the root URL: e.g., https://example.com/
	root := fmt.Sprintf("%s://%s/", scheme, domain)
	fmt.Printf("[DEBUG] Starting crawl for %s\n", root)

	// === Step 2: Initialize tracking variables ===

	// `seen` tracks visited pages to prevent infinite loops
	seen := map[string]bool{root: true}

	// `queue` is the list of URLs to visit
	queue := []string{root}

	// `jsSet` will store discovered JS file URLs
	jsSet := map[string]bool{}

	// === Step 3: Start crawling pages one by one ===
	for len(queue) > 0 {
		// Dequeue the first page from the queue
		page := queue[0]
		queue = queue[1:]

		fmt.Printf("[DEBUG] Crawling page: %s\n", page)

		// Send HTTP GET request to the page
		resp, err := http.Get(page)
		if err != nil {
			// If request fails, show error and move on
			fmt.Printf("[ERROR] Fetch %s: %v\n", page, err)
			continue
		}

		// Read response body content
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close() // Close connection
		if err != nil {
			fmt.Printf("[ERROR] Read %s: %v\n", page, err)
			continue
		}

		// Convert byte array to string
		content := string(body)

		// === Step 4: Extract JavaScript URLs from page ===
		for _, js := range extractJS(content, page) {
			jsSet[js] = true
		}

		// === Step 5: Extract and enqueue internal links ===
		for _, link := range extractLinks(content, page) {
			// Check if link is in same domain and hasn't been visited
			if sameDomain(link, domain) && !seen[link] {
				seen[link] = true
				queue = append(queue, link)
			}
		}
	}

	// === Step 6: Exit if no JS files were found ===
	if len(jsSet) == 0 {
		fmt.Printf("[DEBUG] No JS files found; exiting.\n")
		return
	}

	// === Step 7: Create output files ===
	allFile := fmt.Sprintf("%s_all_js.txt", domain)
	goodFile := fmt.Sprintf("%s_good_js.txt", domain)
	badFile := fmt.Sprintf("%s_bad_js.txt", domain)

	// Create and open files for writing
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

	// Create buffered writers for better performance
	aw := bufio.NewWriter(af)
	gw := bufio.NewWriter(gf)
	bw := bufio.NewWriter(bf)

	// === Step 8: Write all discovered JS URLs to file ===
	for js := range jsSet {
		fmt.Fprintln(aw, js)
	}
	aw.Flush()
	fmt.Printf("[DEBUG] Wrote all JS to %s\n", allFile)

	// === Step 9: Check if each JS file is accessible ===
	fmt.Println("[DEBUG] Testing JS files...")
	for js := range jsSet {
		resp, err := http.Get(js)
		if err != nil {
			// Network error — consider this a bad JS file
			fmt.Printf("[ERROR] Fetch JS %s: %v\n", js, err)
			fmt.Fprintln(bw, js)
			continue
		}
		status := resp.StatusCode
		resp.Body.Close()

		// Flag based on HTTP response code
		if status >= 400 {
			// 400+ codes mean error (bad file)
			fmt.Printf("[FLAG] %s returned %d\n", js, status)
			fmt.Fprintln(bw, js)
		} else {
			// File is valid
			fmt.Printf("[OK]   %s returned %d\n", js, status)
			fmt.Fprintln(gw, js)
		}
	}

	// Flush remaining buffered content to disk
	gw.Flush()
	bw.Flush()

	// === Final report ===
	fmt.Printf("[DEBUG] Good JS in %s, bad JS in %s\n", goodFile, badFile)
	fmt.Printf("[DEBUG] Pages visited: %d, JS files found: %d\n", len(seen), len(jsSet))
}

// === extractJS: Extract .js URLs from script and link tags ===
func extractJS(htmlContent, base string) []string {
	var out []string

	// Parse HTML string into a DOM tree
	doc, err := html.Parse(strings.NewReader(htmlContent))
	if err != nil {
		fmt.Printf("[ERROR] Parse HTML %s: %v\n", base, err)
		return out
	}

	// Recursively traverse the HTML nodes
	var rec func(*html.Node)
	rec = func(n *html.Node) {
		if n.Type == html.ElementNode {
			// Look for <script src="...">
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

			// Look for <link rel="modulepreload" or "prefetch" as="script" href="...">
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

		// Continue traversing child nodes
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			rec(c)
		}
	}

	// Start recursion from root node
	rec(doc)
	return out
}

// === extractLinks: Find all hyperlinks from <a href="..."> ===
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

// === resolveURL: Convert relative URL to absolute based on base page ===
func resolveURL(base, href string) string {
	u, err := url.Parse(href)
	if err != nil {
		return ""
	}
	if u.IsAbs() {
		// Already absolute (has scheme), return as-is
		return u.String()
	}
	bu, err := url.Parse(base)
	if err != nil {
		return ""
	}
	// Join relative path to base
	return bu.ResolveReference(u).String()
}

// === sameDomain: Check if a link belongs to the same domain ===
func sameDomain(link, domain string) bool {
	u, err := url.Parse(link)
	return err == nil && u.Host == domain
}
