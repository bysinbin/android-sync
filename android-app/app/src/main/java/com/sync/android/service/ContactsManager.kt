package com.sync.android.service

import android.Manifest
import android.content.Context
import android.content.pm.PackageManager
import android.provider.ContactsContract
import android.util.Log
import androidx.core.content.ContextCompat
import com.sync.android.model.ContactItem

object ContactsManager {
    private const val TAG = "ContactsManager"

    fun fetchContacts(context: Context, limit: Int = 1000): List<ContactItem> {
        if (ContextCompat.checkSelfPermission(context, Manifest.permission.READ_CONTACTS) != PackageManager.PERMISSION_GRANTED) {
            Log.w(TAG, "READ_CONTACTS izni verilmemiş!")
            return emptyList()
        }

        val contacts = mutableListOf<ContactItem>()
        val seenPairs = mutableSetOf<String>()
        val uri = ContactsContract.CommonDataKinds.Phone.CONTENT_URI
        val projection = arrayOf(
            ContactsContract.CommonDataKinds.Phone.CONTACT_ID,
            ContactsContract.CommonDataKinds.Phone.DISPLAY_NAME,
            ContactsContract.CommonDataKinds.Phone.NUMBER
        )
        val sortOrder = "${ContactsContract.CommonDataKinds.Phone.DISPLAY_NAME} COLLATE NOCASE ASC LIMIT $limit"

        try {
            context.contentResolver.query(uri, projection, null, null, sortOrder)?.use { cursor ->
                val idIdx = cursor.getColumnIndex(ContactsContract.CommonDataKinds.Phone.CONTACT_ID)
                val nameIdx = cursor.getColumnIndex(ContactsContract.CommonDataKinds.Phone.DISPLAY_NAME)
                val numIdx = cursor.getColumnIndex(ContactsContract.CommonDataKinds.Phone.NUMBER)

                while (cursor.moveToNext()) {
                    val id = if (idIdx >= 0) cursor.getString(idIdx) ?: "" else ""
                    val name = if (nameIdx >= 0) cursor.getString(nameIdx) ?: "İsimsiz" else "İsimsiz"
                    var number = if (numIdx >= 0) cursor.getString(numIdx) ?: "" else ""
                    number = number.trim()
                    if (number.isNotEmpty()) {
                        val key = "$name|$number"
                        if (!seenPairs.contains(key)) {
                            seenPairs.add(key)
                            contacts.add(ContactItem(id = id, name = name, number = number))
                        }
                    }
                }
            }
            Log.d(TAG, "${contacts.size} adet kişi rehberden başarıyla okundu.")
        } catch (e: Exception) {
            Log.e(TAG, "Rehber sorgulama hatası: ${e.message}")
        }

        return contacts
    }
}
