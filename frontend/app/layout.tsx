import type { Metadata, Viewport } from "next";
import { Geist, Geist_Mono } from "next/font/google";
import "./globals.css";
import { AppNav } from "@/components/AppNav";
import { TopProgress } from "@/components/TopProgress";

// Geist is a clean, modern product typeface. Loaded self-hosted via next/font
// and exposed as CSS variables that globals.css wires into the Tailwind theme.
const geistSans = Geist({ subsets: ["latin"], variable: "--font-geist-sans", display: "swap" });
const geistMono = Geist_Mono({ subsets: ["latin"], variable: "--font-geist-mono", display: "swap" });

export const metadata: Metadata = {
  title: "EscrowPay",
  description:
    "Bank-native micro-escrow for social commerce: funds are held by the bank until the buyer confirms delivery.",
  applicationName: "EscrowPay",
  appleWebApp: { capable: true, statusBarStyle: "default", title: "EscrowPay" },
};

export const viewport: Viewport = {
  themeColor: "#059669",
  colorScheme: "light",
  width: "device-width",
  initialScale: 1,
  maximumScale: 1,
};

export default function RootLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="en" className={`h-full antialiased ${geistSans.variable} ${geistMono.variable}`}>
      <body className="min-h-full">
        {/* Restore the sidebar collapse preference before paint to avoid a flash. */}
        <script
          dangerouslySetInnerHTML={{
            __html: `try{if(localStorage.getItem('ep-sidebar')==='collapsed')document.documentElement.setAttribute('data-sidebar','collapsed')}catch(e){}`,
          }}
        />
        <TopProgress />
        <AppNav />
        <main className="ep-main flex min-h-[100dvh] flex-col">{children}</main>
      </body>
    </html>
  );
}
