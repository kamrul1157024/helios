import { useState } from 'react';
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card';
import { Button } from '@/components/ui/button';

export function Dashboard() {
  const [downloading, setDownloading] = useState(false);

  function handleDownload() {
    setDownloading(true);
    // Cookie is already set from setup — download is authenticated
    window.location.href = '/api/app/download';
    setTimeout(() => setDownloading(false), 3000);
  }

  return (
    <div className="flex items-center justify-center min-h-screen p-4">
      <Card className="w-full max-w-md">
        <CardHeader className="text-center">
          <CardTitle className="text-2xl">helios</CardTitle>
          <CardDescription>Install the mobile app</CardDescription>
        </CardHeader>
        <CardContent className="space-y-6">
          <div className="text-center space-y-2">
            <div className="inline-flex items-center justify-center w-16 h-16 rounded-2xl bg-primary/10 mb-2">
              <svg className="h-8 w-8 text-primary" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
                <path strokeLinecap="round" strokeLinejoin="round" d="M10.5 1.5H8.25A2.25 2.25 0 006 3.75v16.5a2.25 2.25 0 002.25 2.25h7.5A2.25 2.25 0 0018 20.25V3.75a2.25 2.25 0 00-2.25-2.25H13.5m-3 0V3h3V1.5m-3 0h3m-3 18.75h3" />
              </svg>
            </div>
            <p className="text-sm text-muted-foreground">
              Get the helios app for background notifications — approve or deny tool requests from anywhere.
            </p>
          </div>

          <Button
            onClick={handleDownload}
            disabled={downloading}
            className="w-full h-12 text-base"
            size="lg"
          >
            {downloading ? (
              <span className="flex items-center gap-2">
                <svg className="animate-spin h-4 w-4" viewBox="0 0 24 24" fill="none">
                  <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
                  <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
                </svg>
                Downloading...
              </span>
            ) : (
              <span className="flex items-center gap-2">
                <svg className="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-4l-4 4m0 0l-4-4m4 4V4" />
                </svg>
                Download for Android
              </span>
            )}
          </Button>

          <div className="rounded-lg bg-muted p-4 space-y-3 text-sm">
            <p className="font-medium">How to install:</p>
            <ol className="list-decimal list-inside space-y-1 text-muted-foreground">
              <li>Tap "Download for Android" above</li>
              <li>Open the downloaded APK file</li>
              <li>Allow "Install from unknown sources" if prompted</li>
              <li>Open the helios app</li>
              <li>Scan the "Mobile App" QR code from your terminal</li>
            </ol>
          </div>

          <p className="text-xs text-center text-muted-foreground/60">
            The app connects to your helios daemon via the same tunnel URL and delivers push notifications even when the browser is closed.
          </p>
        </CardContent>
      </Card>
    </div>
  );
}
