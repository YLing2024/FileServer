from PIL import Image
for f in ["testdata_thumb.jpg", "testdata_vthumb.jpg"]:
    img = Image.open(f).convert("RGB")
    px = list(img.getdata())
    colors = len(set(px))
    avg = tuple(sum(c[i] for c in px) // len(px) for i in range(3))
    center = img.getpixel((img.size[0] // 2, img.size[1] // 2))
    print(f, "size:", img.size, "colors:", colors, "avg:", avg, "center:", center)
