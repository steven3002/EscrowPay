import type { Metadata, Viewport } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "EscrowPay",
  description:
    "Bank-native micro-escrow for social commerce: funds are held by the bank until the buyer confirms delivery.",
  applicationName: "EscrowPay",
  appleWebApp: { capable: true, statusBarStyle: "black-translucent", title: "EscrowPay" },
};

export const viewport: Viewport = {
  themeColor: "#059669",
  width: "device-width",
  initialScale: 1,
  maximumScale: 1,
};

export default function RootLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="en" className="h-full antialiased">
      <body className="min-h-full">{children}</body>
    </html>
  );
}
