package response

import "github.com/gin-gonic/gin"

type Envelope struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

func OK(c *gin.Context, data interface{}) {
	c.JSON(200, Envelope{Code: 0, Message: "ok", Data: data})
}

func BadRequest(c *gin.Context, message string) {
	c.JSON(400, Envelope{Code: 400, Message: message})
}

func Unauthorized(c *gin.Context, message string) {
	c.JSON(401, Envelope{Code: 401, Message: message})
}

func ServerError(c *gin.Context, message string) {
	c.JSON(500, Envelope{Code: 500, Message: message})
}
