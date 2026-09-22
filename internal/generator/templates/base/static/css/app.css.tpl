:root {
	font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
	color: #172033;
	background: #f4f7fb;
	line-height: 1.5;
}

* { box-sizing: border-box; }
body { margin: 0; }
a { color: #155eef; text-decoration: none; }
a:hover { text-decoration: underline; }
.site-header { background: #ffffff; border-bottom: 1px solid #d9e1ec; }
.site-nav, .site-main, .site-footer-inner { width: min(100% - 2rem, 72rem); margin: 0 auto; }
.site-nav { display: flex; align-items: center; justify-content: space-between; gap: 1rem; padding: 1rem 0; }
.brand { font-weight: 700; font-size: 1.2rem; }
.nav-links { display: flex; gap: 1rem; }
.site-main { padding: 3rem 0; }
.hero { max-width: 48rem; margin: 0 auto 3rem; text-align: center; }
.hero h1 { margin: 0 0 1rem; font-size: clamp(2.25rem, 6vw, 4rem); line-height: 1.05; }
.hero p { color: #526079; font-size: 1.2rem; }
.accent { color: #155eef; font-weight: 600; }
.feature-grid { display: grid; grid-template-columns: repeat(3, 1fr); gap: 1.5rem; margin-bottom: 3rem; }
.feature, .counter-panel, .prose { background: #ffffff; border: 1px solid #d9e1ec; border-radius: .5rem; padding: 1.5rem; }
.feature-icon { font-size: 1.8rem; margin-bottom: .75rem; }
.feature h2, .counter-panel h2 { margin: 0 0 .5rem; font-size: 1.2rem; }
.feature p, .prose { color: #526079; }
.counter-panel { background: #eef4ff; border-color: #b8cdfb; }
.counter-controls { display: flex; align-items: center; gap: 1rem; }
.button { border: 0; border-radius: .35rem; color: #ffffff; cursor: pointer; padding: .65rem 1rem; font: inherit; }
.button-primary { background: #155eef; }
.button-danger { background: #d92d20; }
.counter-value { font: 1.5rem ui-monospace, SFMono-Regular, Menlo, monospace; }
.prose { max-width: 48rem; margin: 0 auto; }
.prose h1, .prose h2 { color: #172033; }
.prose li + li { margin-top: .5rem; }
.site-footer { margin-top: 4rem; padding: 1.5rem 0; color: #ffffff; background: #172033; }
.site-footer-inner { text-align: center; }

@media (max-width: 40rem) {
	.site-nav { align-items: flex-start; flex-direction: column; }
	.feature-grid { grid-template-columns: 1fr; }
	.counter-controls { flex-wrap: wrap; }
}