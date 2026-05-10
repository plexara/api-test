import React from "react";
import ReactDOM from "react-dom/client";
import { BrowserRouter, Routes, Route, Navigate } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import "./index.css";
import App from "./App";
import Login from "./pages/Login";
import Dashboard from "./pages/Dashboard";
import Endpoints from "./pages/Endpoints";
import Audit from "./pages/Audit";
import ApiKeys from "./pages/ApiKeys";
import Config from "./pages/Config";
import About from "./pages/About";
import { ErrorBoundary } from "./components/ErrorBoundary";
import "./stores/auth";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: { staleTime: 5_000, refetchOnWindowFocus: false, retry: false },
  },
});

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <ErrorBoundary>
      <QueryClientProvider client={queryClient}>
        <BrowserRouter basename="/portal">
          <Routes>
            <Route path="/login" element={<Login />} />
            <Route path="/" element={<App />}>
              <Route index element={<Dashboard />} />
              <Route path="endpoints" element={<Endpoints />} />
              <Route path="endpoints/:name" element={<Endpoints />} />
              <Route path="audit" element={<Audit />} />
              <Route path="keys" element={<ApiKeys />} />
              <Route path="config" element={<Config />} />
              <Route path="about" element={<About />} />
              <Route path="*" element={<Navigate to="/" replace />} />
            </Route>
          </Routes>
        </BrowserRouter>
      </QueryClientProvider>
    </ErrorBoundary>
  </React.StrictMode>
);
