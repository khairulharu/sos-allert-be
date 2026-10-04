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
    INGAT Khairul: ada perubahan rencana dari awal gua pengen store data satu user kayak {UserId, Latitude, Longitude} di databse bisanya ternyata itu kurang buat implementasi proximity, jadi ganti implementasi pakai redis GEOSPATIAL semua kodenya ada ri repository Location mulai dari nyimpen lokasi user, ngambil data GET
    dan ngambil user dari jarak 1km pakai GEOSEARCH implementasinya di internal/repository/location_repostory.go, disitu juga bakalan ada fitur GEOSEARCHNYA jadi gak usah pakai level haversine di bagian usecase pakai fitur GEOSPATIAL dari redis aja wkwkw, mungkin akan ada filterdata di bagian usecase buat GEOSERCHNYA untuk user yang udah gak aktif tapi masuk radius 1km, soalnya fitur GEOSPATIAL gak punya TTL, time to live, data basi maksud gua, oke gua implementasi dulu repositorynya baru di test usecase buat SAVE dan GET terlebih dahulu. Hari ini 4 oktober gua cuman implementasi repository redisnya belum test nanti buatin config redisnya buat test terlbeih dahulu sama test delete user soalnya takutnya gak ada test sama sekali, note juga defauktnya redis itu bakalan minta longitude dulu batu latitude, ubah semua jadi gini, entity sama model
    type UserLocation struct {
	UserId    string
	Longitude float64
	Latitude  float64
}


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
