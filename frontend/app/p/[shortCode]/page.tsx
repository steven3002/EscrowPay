"use client";

import { Suspense } from "react";
import { useParams, useSearchParams } from "next/navigation";
import { Page, Spinner } from "@/components/ui";
import PocketClient from "./PocketClient";

function PocketRoute() {
  const params = useParams<{ shortCode: string }>();
  const token = useSearchParams().get("t") ?? "";
  return <PocketClient shortCode={params.shortCode} token={token} />;
}

export default function PocketPage() {
  return (
    <Suspense
      fallback={
        <Page>
          <div className="flex flex-1 items-center justify-center pt-24 text-muted">
            <Spinner />
          </div>
        </Page>
      }
    >
      <PocketRoute />
    </Suspense>
  );
}
