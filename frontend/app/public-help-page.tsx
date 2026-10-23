"use client"

import { useEffect, useState } from "react"
import { BookOpen } from "lucide-react"
import { useParams } from "react-router-dom"
import { PublicPageHeader } from "@/components/public/public-page-header"
import { HelpDocumentFrame } from "@/components/ui/rich-text-editor"
import { apiFetch } from "@/lib/api"
import { renderHelpTemplate } from "@/lib/help-html"
import type { PublicHelpDocument } from "@/lib/api-types"

export default function PublicHelpPage() {
  const { stationID } = useParams()
  const [data, setData] = useState<PublicHelpDocument | null>(null)
  const [error, setError] = useState("")

  useEffect(() => {
    let active = true
    setData(null)
    setError("")
    if (!stationID) return
    void apiFetch<PublicHelpDocument>(`/public/relay-stations/${stationID}/help`, { skipAuthErrorHandler: true })
      .then((result) => { if (active) setData(result) })
      .catch((e) => { if (active) setError((e as Error).message || "帮助文档读取失败") })
    return () => { active = false }
  }, [stationID])

  useEffect(() => { document.title = data?.title || "帮助文档" }, [data?.title])

  const documentHtml = data ? renderHelpTemplate(data.content, data.station_name, data.station_url) : ""
  return (
    <main data-public-page className="flex h-dvh min-w-0 flex-col overflow-hidden">
      <PublicPageHeader
        icon={BookOpen}
        title="帮助文档"
        description={`${data?.station_name ? `${data.station_name} · ` : ""}API 接入与常见问题`}
        updatedAt={data?.updated_at}
      />
      <section aria-label="帮助文档正文" className="min-h-0 min-w-0 flex-1 overflow-hidden">
        {error ? (
          <p role="alert" className="flex h-full items-center justify-center text-sm text-danger">{error}</p>
        ) : data ? (
          <HelpDocumentFrame html={documentHtml} title={data.title || "完整帮助文档"} hideDocumentHeader className="h-full w-full" />
        ) : (
          <p className="flex h-full items-center justify-center text-sm text-muted-foreground" role="status">正在读取帮助文档…</p>
        )}
      </section>
    </main>
  )
}
