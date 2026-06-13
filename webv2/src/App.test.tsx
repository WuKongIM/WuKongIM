import { render, screen, within } from "@testing-library/react"

import App from "./App"

test("renders the webv2 app shell with sidebar and content regions", () => {
  render(<App />)

  expect(screen.getByRole("banner")).toHaveTextContent("Goroutines")
  expect(screen.getByText("WuKongIM")).toBeInTheDocument()
  expect(screen.getByRole("navigation", { name: "Primary navigation" })).toBeInTheDocument()
  expect(screen.getByRole("main")).toHaveAccessibleName("Content workspace")
})

test("shows only overview and monitor in the primary menu", () => {
  render(<App />)

  const nav = screen.getByRole("navigation", { name: "Primary navigation" })
  expect(within(nav).getByRole("link", { name: "Overview" })).toBeInTheDocument()
  expect(within(nav).getByRole("link", { name: "Monitor" })).toBeInTheDocument()
  expect(within(nav).getByRole("link", { name: "goroutines" })).toHaveAttribute("aria-current", "page")
  expect(within(nav).queryByRole("link", { name: "Nodes" })).not.toBeInTheDocument()
  expect(within(nav).queryByRole("link", { name: "Messages" })).not.toBeInTheDocument()
})

test("keeps the sidebar outside the scrollable content pane", () => {
  render(<App />)

  expect(screen.getByTestId("app-shell")).toHaveClass("h-screen", "overflow-hidden")
  expect(screen.getByRole("navigation", { name: "Primary navigation" })).toHaveClass("shrink-0")
  expect(screen.getByRole("main")).toHaveClass("overflow-y-auto")
})

test("renders the static goroutines distribution monitor page", () => {
  render(<App />)

  expect(screen.getByRole("heading", { name: "Goroutines" })).toBeInTheDocument()
  expect(screen.getByText("System goroutine distribution")).toBeInTheDocument()
  expect(screen.getByText("By subsystem")).toBeInTheDocument()
  expect(screen.getByText("State distribution")).toBeInTheDocument()
  expect(screen.getByText("Top goroutine groups")).toBeInTheDocument()
  expect(screen.getAllByText("Mock data").length).toBeGreaterThan(0)
})
