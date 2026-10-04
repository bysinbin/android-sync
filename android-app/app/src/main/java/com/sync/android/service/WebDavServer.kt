package com.sync.android.service

import android.content.Context
import android.os.Environment
import android.util.Log
import java.io.*
import java.net.ServerSocket
import java.net.Socket
import java.net.URLDecoder
import java.text.SimpleDateFormat
import java.util.*
import java.util.concurrent.ExecutorService
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicBoolean

class WebDavServer(private val context: Context, val port: Int = 8088) {

    companion object {
        private const val TAG = "WebDavServer"
        var instance: WebDavServer? = null
    }

    init {
        instance = this
    }

    private var serverSocket: ServerSocket? = null
    private val isRunning = AtomicBoolean(false)
    private var executor: ExecutorService? = null
    private val rootDir: File = Environment.getExternalStorageDirectory()

    private val rfc1123DateFormat: SimpleDateFormat
        get() = SimpleDateFormat("EEE, dd MMM yyyy HH:mm:ss 'GMT'", Locale.US).apply {
            timeZone = TimeZone.getTimeZone("GMT")
        }

    private val iso8601DateFormat: SimpleDateFormat
        get() = SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ss'Z'", Locale.US).apply {
            timeZone = TimeZone.getTimeZone("GMT")
        }

    fun start(): Boolean {
        if (isRunning.get()) return true
        return try {
            serverSocket = ServerSocket(port)
            isRunning.set(true)
            executor = Executors.newCachedThreadPool()

            executor?.submit {
                Log.d(TAG, "WebDAV sunucusu port $port üzerinde başlatıldı. Kök: ${rootDir.absolutePath}")
                while (isRunning.get() && serverSocket?.isClosed == false) {
                    try {
                        val client = serverSocket?.accept() ?: break
                        executor?.submit { handleClient(client) }
                    } catch (e: Exception) {
                        if (isRunning.get()) Log.w(TAG, "WebDAV accept hatası: ${e.message}")
                    }
                }
            }
            true
        } catch (e: Exception) {
            Log.e(TAG, "WebDAV başlatılamadı: ${e.message}", e)
            false
        }
    }

    fun stop() {
        if (!isRunning.getAndSet(false)) return
        try {
            serverSocket?.close()
            serverSocket = null
            executor?.shutdownNow()
            executor = null
            Log.d(TAG, "WebDAV sunucusu durduruldu.")
        } catch (e: Exception) {
            Log.e(TAG, "WebDAV durdurma hatası: ${e.message}")
        }
    }

    fun isServerRunning(): Boolean = isRunning.get()

    private fun handleClient(socket: Socket) {
        try {
            socket.soTimeout = 15000
            val input = socket.getInputStream()
            val output = socket.getOutputStream()

            val reader = BufferedReader(InputStreamReader(input))
            val requestLine = reader.readLine() ?: return
            val parts = requestLine.split(" ")
            if (parts.size < 2) return

            val method = parts[0].uppercase()
            var rawPath = parts[1]
            if (rawPath.contains("?")) {
                rawPath = rawPath.substringBefore("?")
            }
            val path = URLDecoder.decode(rawPath, "UTF-8")

            val headers = mutableMapOf<String, String>()
            var line: String?
            while (reader.readLine().also { line = it } != null) {
                if (line.isNullOrEmpty()) break
                val colonIdx = line!!.indexOf(":")
                if (colonIdx > 0) {
                    val k = line!!.substring(0, colonIdx).trim().lowercase()
                    val v = line!!.substring(colonIdx + 1).trim()
                    headers[k] = v
                }
            }

            val targetFile = resolveFile(path)

            when (method) {
                "OPTIONS" -> handleOptions(output)
                "PROPFIND" -> handlePropfind(output, path, targetFile, headers["depth"] ?: "1")
                "GET", "HEAD" -> handleGet(output, targetFile, method == "HEAD")
                "PUT" -> handlePut(input, output, targetFile, headers["content-length"]?.toLongOrNull() ?: 0L)
                "MKCOL" -> handleMkcol(output, targetFile)
                "DELETE" -> handleDelete(output, targetFile)
                "MOVE" -> handleMove(output, targetFile, headers["destination"])
                else -> {
                    sendResponse(output, 501, "Not Implemented", "Method not implemented")
                }
            }
        } catch (e: Exception) {
            Log.w(TAG, "İstek işleme hatası: ${e.message}")
        } finally {
            try { socket.close() } catch (_: Exception) {}
        }
    }

    private fun resolveFile(path: String): File {
        val cleanPath = path.trimStart('/')
        return if (cleanPath.isEmpty()) rootDir else File(rootDir, cleanPath)
    }

    private fun handleOptions(out: OutputStream) {
        val headers = ("HTTP/1.1 200 OK\r\n" +
                "DAV: 1, 2\r\n" +
                "MS-Author-Via: DAV\r\n" +
                "Allow: OPTIONS, GET, HEAD, POST, PUT, DELETE, TRACE, PROPFIND, PROPPATCH, MKCOL, COPY, MOVE\r\n" +
                "Content-Length: 0\r\n" +
                "Connection: close\r\n\r\n").toByteArray()
        out.write(headers)
        out.flush()
    }

    private fun handlePropfind(out: OutputStream, reqPath: String, file: File, depth: String) {
        if (!file.exists()) {
            sendResponse(out, 404, "Not Found", "Resource not found")
            return
        }

        val sb = StringBuilder()
        sb.append("<?xml version=\"1.0\" encoding=\"utf-8\" ?>\n")
        sb.append("<D:multistatus xmlns:D=\"DAV:\">\n")

        // Self
        appendFileXml(sb, reqPath, file)

        // Children if directory and depth != 0
        if (file.isDirectory && depth != "0") {
            val children = file.listFiles() ?: emptyArray()
            val basePath = if (reqPath.endsWith("/")) reqPath else "$reqPath/"
            for (child in children) {
                appendFileXml(sb, basePath + child.name, child)
            }
        }

        sb.append("</D:multistatus>")

        val bytes = sb.toString().toByteArray(Charsets.UTF_8)
        val headers = ("HTTP/1.1 207 Multi-Status\r\n" +
                "Content-Type: application/xml; charset=utf-8\r\n" +
                "Content-Length: ${bytes.size}\r\n" +
                "Connection: close\r\n\r\n").toByteArray()
        out.write(headers)
        out.write(bytes)
        out.flush()
    }

    private fun appendFileXml(sb: StringBuilder, path: String, file: File) {
        val isDir = file.isDirectory
        val modDate = rfc1123DateFormat.format(Date(file.lastModified()))
        val crDate = iso8601DateFormat.format(Date(file.lastModified()))
        val safeHref = if (isDir && !path.endsWith("/")) "$path/" else path

        sb.append("  <D:response>\n")
        sb.append("    <D:href>$safeHref</D:href>\n")
        sb.append("    <D:propstat>\n")
        sb.append("      <D:prop>\n")
        sb.append("        <D:displayname>${escapeXml(file.name)}</D:displayname>\n")
        sb.append("        <D:getlastmodified>$modDate</D:getlastmodified>\n")
        sb.append("        <D:creationdate>$crDate</D:creationdate>\n")
        if (isDir) {
            sb.append("        <D:resourcetype><D:collection/></D:resourcetype>\n")
        } else {
            sb.append("        <D:resourcetype/>\n")
            sb.append("        <D:getcontentlength>${file.length()}</D:getcontentlength>\n")
            sb.append("        <D:getcontenttype>application/octet-stream</D:getcontenttype>\n")
        }
        sb.append("      </D:prop>\n")
        sb.append("      <D:status>HTTP/1.1 200 OK</D:status>\n")
        sb.append("    </D:propstat>\n")
        sb.append("  </D:response>\n")
    }

    private fun handleGet(out: OutputStream, file: File, headOnly: Boolean) {
        if (!file.exists()) {
            sendResponse(out, 404, "Not Found", "File not found")
            return
        }

        if (file.isDirectory) {
            val list = file.list() ?: emptyArray()
            val body = "<html><body><h1>Directory: ${file.name}</h1><ul>" +
                    list.joinToString("") { "<li><a href=\"$it\">$it</a></li>" } +
                    "</ul></body></html>"
            val bytes = body.toByteArray(Charsets.UTF_8)
            val headers = ("HTTP/1.1 200 OK\r\n" +
                    "Content-Type: text/html; charset=utf-8\r\n" +
                    "Content-Length: ${bytes.size}\r\n" +
                    "Connection: close\r\n\r\n").toByteArray()
            out.write(headers)
            if (!headOnly) out.write(bytes)
            out.flush()
            return
        }

        val len = file.length()
        val modDate = rfc1123DateFormat.format(Date(file.lastModified()))
        val headers = ("HTTP/1.1 200 OK\r\n" +
                "Content-Type: application/octet-stream\r\n" +
                "Content-Length: $len\r\n" +
                "Last-Modified: $modDate\r\n" +
                "Connection: close\r\n\r\n").toByteArray()
        out.write(headers)

        if (!headOnly) {
            FileInputStream(file).use { fis ->
                val buf = ByteArray(64 * 1024)
                var read: Int
                while (fis.read(buf).also { read = it } != -1) {
                    out.write(buf, 0, read)
                }
            }
        }
        out.flush()
    }

    private fun handlePut(input: InputStream, out: OutputStream, file: File, length: Long) {
        try {
            file.parentFile?.mkdirs()
            FileOutputStream(file).use { fos ->
                val buf = ByteArray(64 * 1024)
                var remaining = length
                var read: Int
                while (remaining > 0) {
                    val toRead = remaining.coerceAtMost(buf.size.toLong()).toInt()
                    read = input.read(buf, 0, toRead)
                    if (read == -1) break
                    fos.write(buf, 0, read)
                    remaining -= read
                }
            }
            sendResponse(out, 201, "Created", "File written successfully")
        } catch (e: Exception) {
            sendResponse(out, 500, "Internal Server Error", e.message ?: "Write error")
        }
    }

    private fun handleMkcol(out: OutputStream, file: File) {
        if (file.exists()) {
            sendResponse(out, 405, "Method Not Allowed", "Collection exists")
            return
        }
        if (file.mkdirs()) {
            sendResponse(out, 201, "Created", "Directory created")
        } else {
            sendResponse(out, 403, "Forbidden", "Cannot create directory")
        }
    }

    private fun handleDelete(out: OutputStream, file: File) {
        if (!file.exists()) {
            sendResponse(out, 404, "Not Found", "File not found")
            return
        }
        if (file.deleteRecursively()) {
            sendResponse(out, 204, "No Content", "")
        } else {
            sendResponse(out, 403, "Forbidden", "Cannot delete")
        }
    }

    private fun handleMove(out: OutputStream, srcFile: File, destHeader: String?) {
        if (destHeader.isNullOrEmpty()) {
            sendResponse(out, 400, "Bad Request", "Missing Destination header")
            return
        }
        try {
            val destUri = java.net.URI(destHeader)
            val destPath = URLDecoder.decode(destUri.path, "UTF-8")
            val destFile = resolveFile(destPath)
            destFile.parentFile?.mkdirs()

            if (srcFile.renameTo(destFile)) {
                sendResponse(out, 201, "Created", "Moved successfully")
            } else {
                sendResponse(out, 403, "Forbidden", "Move failed")
            }
        } catch (e: Exception) {
            sendResponse(out, 500, "Internal Error", e.message ?: "Move error")
        }
    }

    private fun sendResponse(out: OutputStream, code: Int, status: String, body: String) {
        val bytes = body.toByteArray(Charsets.UTF_8)
        val resp = ("HTTP/1.1 $code $status\r\n" +
                "Content-Length: ${bytes.size}\r\n" +
                "Connection: close\r\n\r\n").toByteArray()
        out.write(resp)
        if (bytes.isNotEmpty()) out.write(bytes)
        out.flush()
    }

    private fun escapeXml(s: String): String {
        return s.replace("&", "&amp;")
            .replace("<", "&lt;")
            .replace(">", "&gt;")
            .replace("\"", "&quot;")
            .replace("'", "&apos;")
    }
}
