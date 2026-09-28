# SOS-ALLERT FEATURE


Diagram Concept Sederhana

User/Allert > Klik Tombol SOS Allert > akan memberikan notifikasi ke semua user by radius (1km) > dan tampilan user akan muncul notifikasi realtime > dan user bisa melihat lokasi allert/pelapor dimana,

Kerjakan Dari sini Dahulu Logicnya dan coba buat test case
1. Real Time Location
    Isinya adalah bakalan munculin notifikasi ke seluruh pengguna dengan jarak maksimal 1KM dari allert
    Sub Feture Yang Terikat Di Bawah ini

    1. Buat 1 function dimana dia bisa calculte dalam satuan kilometer kalau dia dekat return true atau false dan return user tersebut
    func CalculateDistance
        Bakal nerima dua lokasi yang berbeda dan ngebandingin kalau itu jaraknya dalam radius 1km atau nggak
2. Notification
    Real time Notification, semua user yang masuk ke dalam radius dari allert
3. GMAPS/Tools penampil Lokasi
    Kita butuh tampilan dimana lokasi kita dan lokasi pengguna sekitar
4. Location Calculation
    Kalkulasi Lokasi berdasarkan Radius 1KM
5. 

Tech Stack
Golang
Supabase/PostgreSQL
Firebase
Svelte
Redis
SMTP
