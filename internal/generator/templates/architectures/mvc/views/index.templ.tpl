package views

templ Index(projectName string) {
	@Layout("Home", projectName) {
		<div>
			<div class="hero">
				<h1>
					Welcome to { projectName }
				</h1>
				<p>
					A high-performance SSR application powered by <span class="accent">FGOTHS</span>
				</p>
			</div>

			<div class="feature-grid">
				<div class="feature">
					<div class="feature-icon">⚡</div>
					<h2>Lightning Fast</h2>
					<p>FlatBuffers for zero-copy serialization and optimal performance</p>
				</div>
				<div class="feature">
					<div class="feature-icon">◇</div>
					<h2>Modern Stack</h2>
					<p>Templ + HTMX for reactive UIs</p>
				</div>
				<div class="feature">
					<div class="feature-icon">▣</div>
					<h2>Single Binary</h2>
					<p>Deploy to scratch containers with minimal footprint</p>
				</div>
			</div>

			<div class="counter-panel">
				<h2>HTMX Demo: Counter</h2>
				<div class="counter-controls">
					<button
						hx-post="/api/counter/increment"
						hx-target="#counter"
						hx-swap="innerHTML"
						class="button button-primary"
					>
						Increment
					</button>
					<span id="counter" class="counter-value">0</span>
					<button
						hx-post="/api/counter/decrement"
						hx-target="#counter"
						hx-swap="innerHTML"
						class="button button-danger"
					>
						Decrement
					</button>
				</div>
			</div>
		</div>
	}
}
