import type { Metadata } from "next";
import { Geist, Geist_Mono } from "next/font/google";
import Link from "next/link";
import "./globals.css";

const geistSans = Geist({
  variable: "--font-geist-sans",
  subsets: ["latin"],
});

const geistMono = Geist_Mono({
  variable: "--font-geist-mono",
  subsets: ["latin"],
});

export const metadata: Metadata = {
  title: "Nagger Dashboard",
  description: "Family task and expense tracking",
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html
      lang="en"
      className={`${geistSans.variable} ${geistMono.variable} h-full antialiased`}
    >
      <body className="min-h-full flex flex-col">
        <nav className="border-b bg-background sticky top-0 z-10">
          <div className="max-w-6xl mx-auto px-4 flex items-center h-12 gap-6">
            <span className="font-bold text-sm">Nagger</span>
            <Link
              href="/finances"
              className="text-sm text-muted-foreground hover:text-foreground transition-colors"
            >
              Finances
            </Link>
            <Link
              href="/tasks"
              className="text-sm text-muted-foreground hover:text-foreground transition-colors"
            >
              Tasks
            </Link>
          </div>
        </nav>
        <main className="flex-1 max-w-6xl mx-auto px-4 py-6 w-full">
          {children}
        </main>
      </body>
    </html>
  );
}
